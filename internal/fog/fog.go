// Package fog는 기상청 API허브 방재기상관측(AWS) 시정자료로 장소 근처의 지금 안개 여부를 구한다.
//
// nph-aws2_min_vis?stn=0은 전국 시정 관측소(약 270곳)의 최신 1분 자료를 한 번에 준다(위경도 포함).
// 그래서 전국 표를 10분마다 한 번만 받아 기억해 두고, 요청마다 가장 가까운 관측소를 고른다.
// 하루 최대 144번 부른다(일반회원 한도는 계정 전체 하루 20,000번).
//
// 안개로 보는 기준: 가까운 관측소(MaxKm 안)의 1분 평균 시정이 1km 미만이거나, 현천 코드가 30(안개).
// 키는 API허브 authKey(data.go.kr 키와 다르다). 값은 어디에도 출력하지 않는다.
package fog

import (
	"bufio"
	"context"
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
	DefaultBaseURL = "https://apihub.kma.go.kr/api/typ01/cgi-bin/url/nph-aws2_min_vis"
	// MaxKm보다 먼 관측소는 쓰지 않는다(안개는 아주 국지적이다).
	MaxKm = 15.0
	// FogVis는 안개로 보는 시정(m) 상한. 기상 관측 기준(시정 1km 미만).
	FogVis  = 1000
	refresh = 10 * time.Minute
	// 자료가 이보다 오래되면 쓰지 않는다.
	maxAge = 40 * time.Minute
)

// Now는 응답에 넣는 지금 안개 정보다.
type Now struct {
	Station int       `json:"station"`
	At      time.Time `json:"at"`
	Km      float64   `json:"km"`  // 장소에서 관측소까지
	Vis     int       `json:"vis"` // 1분 평균 시정(m)
	Fog     bool      `json:"fog"`
}

type obs struct {
	at       time.Time
	stn      int
	lat, lon float64
	vis      int // -1 = 없음
	ww       int // -1 = 없음
}

// Client는 전국 시정 표를 받고 기억한다.
type Client struct {
	base string
	key  string
	hc   *http.Client
	log  *slog.Logger
	now  func() time.Time

	mu      sync.Mutex
	table   []obs
	fetched time.Time
	failed  time.Time
}

// New는 Client를 만든다. key가 비면 nil(안개 정보 없음).
func New(base, key string, log *slog.Logger) *Client {
	if key == "" {
		return nil
	}
	if base == "" {
		base = DefaultBaseURL
	}
	if log == nil {
		log = slog.Default()
	}
	return &Client{base: base, key: key, log: log, now: kst.Now, hc: &http.Client{Timeout: 10 * time.Second}}
}

// Get은 위경도 근처의 지금 안개 정보다. 근처 관측소가 없거나 자료를 못 받으면 nil.
func (c *Client) Get(ctx context.Context, lat, lon float64) *Now {
	if c == nil || (lat == 0 && lon == 0) {
		return nil
	}
	t := c.tableNow(ctx)
	now := c.now()
	var best *obs
	bestKm := math.Inf(1)
	for i := range t {
		o := &t[i]
		if o.vis < 0 && o.ww < 0 {
			continue
		}
		if now.Sub(o.at) > maxAge {
			continue
		}
		if d := km(lat, lon, o.lat, o.lon); d < bestKm {
			best, bestKm = o, d
		}
	}
	if best == nil || bestKm > MaxKm {
		return nil
	}
	n := &Now{Station: best.stn, At: best.at, Km: math.Round(bestKm*10) / 10, Vis: best.vis}
	n.Fog = (best.vis >= 0 && best.vis < FogVis) || best.ww == 30
	return n
}

func (c *Client) tableNow(ctx context.Context) []obs {
	now := c.now()
	c.mu.Lock()
	if now.Sub(c.fetched) < refresh || now.Sub(c.failed) < 2*time.Minute {
		t := c.table
		c.mu.Unlock()
		return t
	}
	c.mu.Unlock()
	t, err := c.fetch(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.failed = now
		c.log.Warn("fog: fetch", "err", err)
		return c.table
	}
	c.table, c.fetched = t, now
	return t
}

func (c *Client) fetch(ctx context.Context) ([]obs, error) {
	q := url.Values{"stn": {"0"}, "disp": {"0"}, "help": {"0"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"?"+q.Encode()+"&authKey="+url.QueryEscape(c.key), nil)
	if err != nil {
		return nil, errors.New("request")
	}
	res, err := c.hc.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("%s", strings.ReplaceAll(ue.Err.Error(), c.key, "***"))
		}
		return nil, errors.New("request failed")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", res.StatusCode)
	}
	return parse(io.LimitReader(res.Body, 2<<20))
}

// parse는 "YYMMDDHHMI STN LON LAT S VIS1 VIS10 WW1 WW15" 줄들을 읽는다. #로 시작하는 줄은 머리말.
func parse(r io.Reader) ([]obs, error) {
	var out []obs
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			if strings.Contains(line, `"status"`) {
				return nil, errors.New("api error")
			}
			continue
		}
		f := strings.Fields(line)
		if len(f) < 9 {
			continue
		}
		at, err := time.ParseInLocation("200601021504", f[0], kst.Loc)
		if err != nil {
			continue
		}
		stn, _ := strconv.Atoi(f[1])
		lon, e1 := strconv.ParseFloat(f[2], 64)
		lat, e2 := strconv.ParseFloat(f[3], 64)
		if e1 != nil || e2 != nil {
			continue
		}
		o := obs{at: at, stn: stn, lat: lat, lon: lon, vis: num(f[5]), ww: num(f[7])}
		out = append(out, o)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("no rows")
	}
	return out, nil
}

// num: -999 등 음수는 없음(-1)
func num(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return -1
	}
	return v
}

// km은 두 위경도 사이 거리(구면 근사)다.
func km(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0
	p := math.Pi / 180
	dlat, dlon := (lat2-lat1)*p, (lon2-lon1)*p
	a := math.Sin(dlat/2)*math.Sin(dlat/2) + math.Cos(lat1*p)*math.Cos(lat2*p)*math.Sin(dlon/2)*math.Sin(dlon/2)
	return 2 * r * math.Asin(math.Sqrt(a))
}
