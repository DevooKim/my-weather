// Package air는 에어코리아(한국환경공단) 대기오염정보로 장소의 지금 미세먼지 등급을 구한다.
//
// 흐름: 위경도 → TM 좌표(여기서 계산) → 근접 측정소(측정소정보 getNearbyMsrstnList) →
// 측정소별 실시간 측정값(대기오염정보 getMsrstnAcctoRltmMesureDnsty).
// TM 좌표를 에어코리아 getTMStdrCrdnt로 받지 않는 이유: 그 API는 법정동 이름만 받는데 우리 행정구역은 행정동이라
// "청운효자동" 같은 곳을 찾지 못한다.
//
// 호출 한도(개발계정)는 기능마다 하루 500건이라 아끼는 쪽으로 만든다.
//   - 장소(행정구역 코드)→측정소는 처음 한 번만 찾고 프로세스가 살아 있는 동안 기억한다.
//   - 측정값은 측정소마다 기억해 두고, 새 시각 자료가 나올 때쯤(매시 15분 이후)에만 다시 받는다.
//   - 기능마다 하루 DailyLimit건을 넘으면 부르지 않는다(기억해 둔 값만 쓴다).
//
// 키는 기상청과 같은 data.go.kr serviceKey다(계정 단위). 값은 어디에도 출력하지 않는다.
package air

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"myweather/internal/kst"
)

const (
	DefaultBaseURL = "https://apis.data.go.kr/B552584"
	// DailyLimit은 기능마다 하루에 부를 최대 횟수다(한도 500건에서 여유를 둔다).
	DailyLimit = 450
	// 측정값을 다시 받는 간격: 같은 시각 자료면 이보다 자주 부르지 않는다.
	refreshMin = 20 * time.Minute
)

// Now는 응답에 넣는 지금 대기질이다. 등급은 1 좋음, 2 보통, 3 나쁨, 4 매우나쁨.
type Now struct {
	Station   string    `json:"station"`
	At        time.Time `json:"at"`
	PM10      *int      `json:"pm10"`
	PM25      *int      `json:"pm25"`
	PM10Grade *int      `json:"pm10_grade"`
	PM25Grade *int      `json:"pm25_grade"`
	// Grade는 두 등급 중 나쁜 쪽, Label은 그 이름("미세먼지" 또는 "초미세먼지")
	Grade int    `json:"grade"`
	Label string `json:"label"`
	Text  string `json:"text"` // 좋음·보통·나쁨·매우나쁨
}

// Place는 측정소를 찾을 장소다.
type Place struct {
	Code     string // 기억 키(행정구역 코드 또는 격자)
	Lat, Lon float64
}

// Client는 에어코리아 조회와 기억을 맡는다.
type Client struct {
	base string
	key  string
	hc   *http.Client
	log  *slog.Logger
	now  func() time.Time

	mu       sync.Mutex
	stations map[string][]string // 행정구역 코드 → 가까운 측정소 이름들(가까운 순)
	noStn    map[string]time.Time
	meas     map[string]*measured // 측정소 → 측정값
	calls    map[string]int       // 기능 → 오늘 호출 수
	day      int64
}

type measured struct {
	now      *Now
	fetched  time.Time
	roadside bool
}

// New는 Client를 만든다. base가 비면 DefaultBaseURL.
func New(base, key string, log *slog.Logger) *Client {
	if base == "" {
		base = DefaultBaseURL
	}
	if log == nil {
		log = slog.Default()
	}
	return &Client{
		base: strings.TrimRight(base, "/"), key: key, log: log, now: kst.Now,
		hc:       &http.Client{Timeout: 8 * time.Second},
		stations: map[string][]string{}, noStn: map[string]time.Time{}, meas: map[string]*measured{}, calls: map[string]int{},
	}
}

var gradeText = map[int]string{1: "좋음", 2: "보통", 3: "나쁨", 4: "매우나쁨"}

// Get은 장소의 지금 대기질이다. 모르면 nil(오류는 기록만 한다).
func (c *Client) Get(ctx context.Context, p Place) *Now {
	if c == nil || p.Code == "" || (p.Lat == 0 && p.Lon == 0) {
		return nil
	}
	stns, err := c.stationsFor(ctx, p)
	if err != nil {
		c.log.Warn("air: station lookup", "region", p.Code, "err", err)
		return nil
	}
	// 가까운 측정소부터: 도로변 측정소는 차 매연 영향이 커서, 3곳 안에 도시대기 측정소가 있으면 그쪽을 쓴다
	var fallback *Now
	for i, st := range stns {
		if i >= 3 {
			break
		}
		m, err := c.measure(ctx, st)
		if err != nil {
			c.log.Warn("air: measure", "station", st, "err", err)
			continue
		}
		if m == nil || m.now == nil {
			continue
		}
		if !m.roadside {
			return m.now
		}
		if fallback == nil {
			fallback = m.now
		}
	}
	return fallback
}

func (c *Client) stationsFor(ctx context.Context, p Place) ([]string, error) {
	c.mu.Lock()
	if s, ok := c.stations[p.Code]; ok {
		c.mu.Unlock()
		return s, nil
	}
	if t, ok := c.noStn[p.Code]; ok && c.now().Sub(t) < 6*time.Hour {
		c.mu.Unlock()
		return nil, nil
	}
	c.mu.Unlock()

	x, y := ToTM(p.Lat, p.Lon)
	var near struct {
		Items []struct {
			StationName string  `json:"stationName"`
			TM          float64 `json:"tm"`
		} `json:"items"`
	}
	if err := c.call(ctx, "MsrstnInfoInqireSvc/getNearbyMsrstnList", url.Values{"tmX": {strconv.FormatFloat(x, 'f', 1, 64)}, "tmY": {strconv.FormatFloat(y, 'f', 1, 64)}, "ver": {"1.1"}}, &near); err != nil {
		return nil, err
	}
	var names []string
	for _, it := range near.Items {
		if it.StationName != "" {
			names = append(names, it.StationName)
		}
	}
	c.mu.Lock()
	if len(names) > 0 {
		c.stations[p.Code] = names
	} else {
		c.noStn[p.Code] = c.now()
	}
	c.mu.Unlock()
	return names, nil
}

// measure는 측정소의 최신 측정값이다. 기억해 둔 값이 충분히 새것이면 부르지 않는다.
func (c *Client) measure(ctx context.Context, station string) (*measured, error) {
	now := c.now()
	c.mu.Lock()
	m := c.meas[station]
	c.mu.Unlock()
	if m != nil && !c.stale(m, now) {
		return m, nil
	}
	var r struct {
		Items []struct {
			DataTime    string `json:"dataTime"`
			MangName    string `json:"mangName"`
			PM10Value   string `json:"pm10Value"`
			PM25Value   string `json:"pm25Value"`
			PM10Grade1h string `json:"pm10Grade1h"`
			PM25Grade1h string `json:"pm25Grade1h"`
		} `json:"items"`
	}
	err := c.call(ctx, "ArpltnInforInqireSvc/getMsrstnAcctoRltmMesureDnsty",
		url.Values{"stationName": {station}, "dataTerm": {"DAILY"}, "ver": {"1.3"}, "numOfRows": {"3"}, "pageNo": {"1"}}, &r)
	if err != nil {
		if m != nil {
			return m, nil // 이전 값이라도 쓴다
		}
		return nil, err
	}
	nm := &measured{fetched: now}
	// 최신 시각부터: 통신장애 등으로 값이 '-'인 시각은 건너뛴다(3시간 안)
	for _, it := range r.Items {
		nm.roadside = it.MangName == "도로변대기"
		at, ok := parseDataTime(it.DataTime)
		if !ok {
			continue
		}
		pm10, pm25 := atoi(it.PM10Value), atoi(it.PM25Value)
		g10, g25 := atoi(it.PM10Grade1h), atoi(it.PM25Grade1h)
		if g10 == nil && g25 == nil {
			continue
		}
		n := &Now{Station: station, At: at, PM10: pm10, PM25: pm25, PM10Grade: g10, PM25Grade: g25, Label: "미세먼지"}
		if g10 != nil {
			n.Grade = *g10
		}
		if g25 != nil && *g25 > n.Grade {
			n.Grade, n.Label = *g25, "초미세먼지"
		}
		n.Text = gradeText[n.Grade]
		nm.now = n
		break
	}
	c.mu.Lock()
	c.meas[station] = nm
	c.mu.Unlock()
	return nm, nil
}

// stale: 다시 받을 때인지. 자료는 매시 정각 값이 15분쯤 뒤에 나온다.
func (c *Client) stale(m *measured, now time.Time) bool {
	if now.Sub(m.fetched) < refreshMin {
		return false
	}
	if m.now == nil {
		return true
	}
	// 이번 시각 자료를 이미 갖고 있으면 다음 시각까지 기다린다
	cur := now.Truncate(time.Hour)
	if now.Sub(cur) < 15*time.Minute {
		cur = cur.Add(-time.Hour)
	}
	return m.now.At.Before(cur)
}

// parseDataTime: "2026-11-10 24:00"(24시는 다음 날 0시)
func parseDataTime(s string) (time.Time, bool) {
	d, hm, ok := strings.Cut(strings.TrimSpace(s), " ")
	if !ok {
		return time.Time{}, false
	}
	day, err := time.ParseInLocation("2006-01-02", d, kst.Loc)
	if err != nil {
		return time.Time{}, false
	}
	h, _, ok := strings.Cut(hm, ":")
	hh, err := strconv.Atoi(h)
	if !ok || err != nil || hh < 0 || hh > 24 {
		return time.Time{}, false
	}
	return day.Add(time.Duration(hh) * time.Hour), true
}

func atoi(s string) *int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return nil
	}
	return &v
}

var errBudget = errors.New("air: daily call limit reached")

// call은 기능 하나를 부르고 response.body를 out에 푼다.
func (c *Client) call(ctx context.Context, op string, q url.Values, out any) error {
	now := c.now()
	day := kst.Day(now)
	c.mu.Lock()
	if c.day != day {
		c.day, c.calls = day, map[string]int{}
	}
	if c.calls[op] >= DailyLimit {
		c.mu.Unlock()
		return errBudget
	}
	c.calls[op]++
	c.mu.Unlock()

	q.Set("returnType", "json")
	// serviceKey는 이미 인코딩된 값이라 그대로 붙인다(다시 인코딩하면 안 된다)
	u := c.base + "/" + op + "?" + q.Encode() + "&serviceKey=" + c.key
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("%s: request", op)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", op, redact(err, c.key))
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%s: read: %w", op, err)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: http %d", op, res.StatusCode)
	}
	var env struct {
		Response struct {
			Header struct {
				ResultCode string `json:"resultCode"`
				ResultMsg  string `json:"resultMsg"`
			} `json:"header"`
			Body json.RawMessage `json:"body"`
		} `json:"response"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		return fmt.Errorf("%s: not json", op)
	}
	if env.Response.Header.ResultCode != "00" {
		return fmt.Errorf("%s: %s %s", op, env.Response.Header.ResultCode, env.Response.Header.ResultMsg)
	}
	if err := json.Unmarshal(env.Response.Body, out); err != nil {
		return fmt.Errorf("%s: body: %w", op, err)
	}
	return nil
}

// redact는 오류 문구에 URL(키 포함)이 들어가지 않게 한다.
func redact(err error, key string) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s", strings.ReplaceAll(ue.Err.Error(), key, "***"))
	}
	return errors.New(strings.ReplaceAll(err.Error(), key, "***"))
}

// Calls는 오늘 기능별 호출 수다(관리자 화면·로그용).
func (c *Client) Calls() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.calls))
	for k, v := range c.calls {
		out[k] = v
	}
	return out
}

// ToTM은 위경도(WGS84)를 에어코리아가 쓰는 TM 중부원점 좌표(미터)로 바꾼다.
// 원점 북위 38°·동경 127°, 축척 1, 가산 (200000, 500000). 타원체는 GRS80으로 근사한다
// (에어코리아는 Bessel 기준이라 수백 m 어긋나지만 가까운 측정소를 고르는 데는 충분하다).
func ToTM(lat, lon float64) (x, y float64) {
	const (
		a   = 6378137.0
		f   = 1 / 298.257222101
		k0  = 1.0
		fe  = 200000.0
		fn  = 500000.0
		la0 = 38.0
		lo0 = 127.0
	)
	e2 := f * (2 - f)
	ep2 := e2 / (1 - e2)
	rad := math.Pi / 180
	phi, lam := lat*rad, lon*rad
	phi0, lam0 := la0*rad, lo0*rad
	m := func(p float64) float64 {
		e4, e6 := e2*e2, e2*e2*e2
		return a * ((1-e2/4-3*e4/64-5*e6/256)*p - (3*e2/8+3*e4/32+45*e6/1024)*math.Sin(2*p) +
			(15*e4/256+45*e6/1024)*math.Sin(4*p) - (35*e6/3072)*math.Sin(6*p))
	}
	n := a / math.Sqrt(1-e2*math.Sin(phi)*math.Sin(phi))
	t := math.Tan(phi) * math.Tan(phi)
	c := ep2 * math.Cos(phi) * math.Cos(phi)
	A := (lam - lam0) * math.Cos(phi)
	x = fe + k0*n*(A+(1-t+c)*A*A*A/6+(5-18*t+t*t+72*c-58*ep2)*A*A*A*A*A/120)
	y = fn + k0*(m(phi)-m(phi0)+n*math.Tan(phi)*(A*A/2+(5-t+9*c+4*c*c)*A*A*A*A/24+(61-58*t+t*t+600*c-330*ep2)*A*A*A*A*A*A/720))
	return x, y
}
