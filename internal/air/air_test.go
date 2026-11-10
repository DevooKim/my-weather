package air

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"myweather/internal/kst"
)

type fake struct {
	mu    sync.Mutex
	calls map[string]int
	data  map[string]string // 측정소 → 측정값 items JSON
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	op := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	f.calls[op]++
	f.mu.Unlock()
	if r.URL.Query().Get("serviceKey") != "KEY%2B1" && r.URL.RawQuery[strings.Index(r.URL.RawQuery, "serviceKey=")+11:] != "KEY%2B1" {
		http.Error(w, "bad key", 403)
		return
	}
	body := ""
	switch op {
	case "getNearbyMsrstnList":
		// 서교동(에어코리아 TM 192755, 450178) 근처면 측정소 두 곳
		x, _ := strconv.ParseFloat(r.URL.Query().Get("tmX"), 64)
		y, _ := strconv.ParseFloat(r.URL.Query().Get("tmY"), 64)
		if math.Abs(x-192755) > 1500 || math.Abs(y-450178) > 1500 {
			body = `{"items":[]}`
		} else {
			body = `{"items":[{"stationName":"도로","tm":1.0},{"stationName":"도시","tm":1.5}]}`
		}
	case "getMsrstnAcctoRltmMesureDnsty":
		body = f.data[r.URL.Query().Get("stationName")]
	}
	fmt.Fprintf(w, `{"response":{"header":{"resultCode":"00","resultMsg":"NORMAL_CODE"},"body":%s}}`, body)
}

func newTest(t *testing.T, now *time.Time) (*Client, *fake) {
	f := &fake{calls: map[string]int{}, data: map[string]string{
		"도로": `{"items":[{"dataTime":"2026-11-10 24:00","mangName":"도로변대기","pm10Value":"90","pm25Value":"40","pm10Grade1h":"3","pm25Grade1h":"3"}]}`,
		"도시": `{"items":[{"dataTime":"2026-11-10 24:00","mangName":"도시대기","pm10Value":"-","pm25Value":"-","pm10Grade1h":"","pm25Grade1h":""},{"dataTime":"2026-11-10 23:00","mangName":"도시대기","pm10Value":"35","pm25Value":"30","pm10Grade1h":"2","pm25Grade1h":"3"}]}`,
	}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c := New(srv.URL, "KEY%2B1", nil)
	c.now = func() time.Time { return *now }
	return c, f
}

var samp = Place{Code: "1144066000", Lat: 37.5497, Lon: 126.9230}

func TestGetPrefersUrbanStationAndCaches(t *testing.T) {
	now := time.Date(2026, 11, 11, 0, 40, 0, 0, kst.Loc)
	c, f := newTest(t, &now)
	n := c.Get(context.Background(), samp)
	if n == nil {
		t.Fatal("nil")
	}
	// 도로변 측정소보다 도시대기 측정소, 최신 시각이 '-'면 그 앞 시각
	if n.Station != "도시" || *n.PM10 != 35 || n.Grade != 3 || n.Label != "초미세먼지" || n.Text != "나쁨" {
		t.Fatalf("%+v", n)
	}
	if !n.At.Equal(time.Date(2026, 11, 10, 23, 0, 0, 0, kst.Loc)) {
		t.Fatalf("at %v", n.At)
	}
	// 10분 뒤: 다시 부르지 않는다
	now = now.Add(10 * time.Minute)
	c.Get(context.Background(), samp)
	if f.calls["getNearbyMsrstnList"] != 1 || f.calls["getMsrstnAcctoRltmMesureDnsty"] != 2 {
		t.Fatalf("calls %v", f.calls)
	}
	// 01:10: 도로(0시 자료)는 이번 시각 것이라 그대로, 도시(23시 자료)만 다시 받는다
	now = now.Add(20 * time.Minute)
	c.Get(context.Background(), samp)
	if f.calls["getMsrstnAcctoRltmMesureDnsty"] != 3 {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestParseDataTime(t *testing.T) {
	at, ok := parseDataTime("2026-11-10 24:00")
	if !ok || !at.Equal(time.Date(2026, 11, 11, 0, 0, 0, 0, kst.Loc)) {
		t.Fatalf("%v %v", at, ok)
	}
	if _, ok := parseDataTime("-"); ok {
		t.Fatal("should fail")
	}
}

func TestDailyLimit(t *testing.T) {
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, kst.Loc)
	c, _ := newTest(t, &now)
	c.calls["MsrstnInfoInqireSvc/getNearbyMsrstnList"] = DailyLimit
	c.day = kst.Day(now)
	if n := c.Get(context.Background(), samp); n != nil {
		t.Fatalf("should be nil over limit: %+v", n)
	}
}

func TestToTM(t *testing.T) {
	// 우리 행정구역 표의 서교동 위경도 → 에어코리아 getTMStdrCrdnt의 서교동 TM (192755, 450178)과 2km 안.
	// 두 기준점 위치가 다르고(동 대표점) 타원체도 근사라 1~2km 차이는 난다. 측정소 간격(수 km)보다는 작다.
	x, y := ToTM(37.549656, 126.922975)
	if math.Hypot(x-192755, y-450178) > 2000 {
		t.Fatalf("tm %.0f %.0f", x, y)
	}
}
