package fog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"myweather/internal/kst"
)

const sample = `#START7777
# YYMMDDHHMI   STN         LON.         LAT.  S   VIS1  VIS10    WW1   WW15
#        KST    ID        (deg)        (deg)       (m)    (m)
202611110620   108 126.96579742  37.57141876  1    640   -999     30   -999
202611110620   119 126.98300171  37.25746155  2  21140   -999   -999   -999
202611110620   127 127.95249939  36.97045135  2   7622   -999      4   -999
202611110620   999 126.92000000  37.55000000  2   -999   -999   -999   -999
#7777END
`

func newTest(t *testing.T, now *time.Time) (*Client, *int32) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		if r.URL.Query().Get("authKey") != "K" {
			w.WriteHeader(403)
			return
		}
		fmt.Fprint(w, sample)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "K", nil)
	c.now = func() time.Time { return *now }
	return c, &n
}

func TestGet(t *testing.T) {
	now := time.Date(2026, 11, 11, 6, 25, 0, 0, kst.Loc)
	c, calls := newTest(t, &now)
	// 서울 종로(108 관측소 2km): 시정 640m, 현천 30 → 안개
	if n := c.Get(context.Background(), 37.584, 126.970); n == nil || !n.Fog || n.Station != 108 || n.Vis != 640 {
		t.Fatalf("seoul %+v", n)
	}
	// 수원(119 근처): 시정 21km → 안개 아님
	if n := c.Get(context.Background(), 37.26, 126.99); n == nil || n.Fog || n.Station != 119 {
		t.Fatalf("suwon %+v", n)
	}
	// 값 없는 관측소(999)는 건너뛰고, 15km 안에 다른 관측소가 없으면 nil
	if n := c.Get(context.Background(), 37.55, 126.92); n != nil && n.Station == 999 {
		t.Fatalf("empty station used %+v", n)
	}
	// 제주: 근처 관측소 없음
	if n := c.Get(context.Background(), 33.5, 126.5); n != nil {
		t.Fatalf("jeju %+v", n)
	}
	if *calls != 1 {
		t.Fatalf("calls %d", *calls)
	}
	// 10분 지나면 다시 받는다
	now = now.Add(11 * time.Minute)
	c.Get(context.Background(), 37.584, 126.970)
	if *calls != 2 {
		t.Fatalf("calls %d", *calls)
	}
	// 자료가 40분 넘게 오래되면 쓰지 않는다
	now = now.Add(40 * time.Minute)
	if n := c.Get(context.Background(), 37.584, 126.970); n != nil {
		t.Fatalf("stale %+v", n)
	}
}

func TestNilWithoutKey(t *testing.T) {
	if New("", "", nil) != nil {
		t.Fatal("want nil")
	}
	var c *Client
	if c.Get(context.Background(), 37, 127) != nil {
		t.Fatal("nil client")
	}
}
