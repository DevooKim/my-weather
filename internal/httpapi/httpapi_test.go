package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"myweather/internal/budget"
	"myweather/internal/collector"
	"myweather/internal/config"
	"myweather/internal/grid"
	"myweather/internal/kma"
	"myweather/internal/kma/kmatest"
	"myweather/internal/kst"
	"myweather/internal/ratelimit"
	"myweather/internal/store"
)

var regions = grid.MustLoad()

type env struct {
	t    *testing.T
	kma  *kmatest.Server
	db   *store.DB
	col  *collector.Collector
	bud  *budget.Budget
	h    http.Handler
	logs *syncBuf
	now  time.Time
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, logs: &syncBuf{}, now: time.Date(2026, 11, 10, 14, 20, 0, 0, kst.Loc)}
	e.kma = kmatest.NewServer(nil)
	t.Cleanup(e.kma.Close)
	db, err := store.Open(filepath.Join(t.TempDir(), "h.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.db = db
	log := slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	clock := func() time.Time { return e.now }
	e.bud = budget.New(10000, clock, log)
	api := kma.New(e.kma.URL, kmatest.Key, kma.Options{PerSecond: 10000, Timeout: 2 * time.Second})
	e.col = collector.New(db, api, e.bud, collector.Options{
		Grids: []config.Grid{{NX: 59, NY: 126}}, Log: log, Now: clock,
	})
	t.Cleanup(e.col.WaitBackground)
	e.h = New(Options{
		DB: db, Collector: e.col, Budget: e.bud, Regions: regions, Version: "test", Now: clock, Log: log,
		IP: &ratelimit.ClientIP{TrustHeader: "X-Forwarded-For", Log: log},
	}).Handler()
	return e
}

// get은 프록시를 거친 요청처럼 loopback + X-Forwarded-For로 보낸다. ip가 ""이면 서버 안(local) 요청.
func (e *env) get(path, ip string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	r.RemoteAddr = "127.0.0.1:40000"
	if ip != "" {
		r.Header.Set("X-Forwarded-For", ip)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("json: %v (%s)", err, w.Body.String())
	}
	return m
}

func TestHealth(t *testing.T) {
	e := newEnv(t)
	if w := e.get("/healthz", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("healthz %d %s", w.Code, w.Body)
	}
	// 바깥 요청에는 상세(수집 격자)를 주지 않음
	w := e.get("/api/health", "203.0.113.9")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"ok":true}` {
		t.Fatalf("public health %s", w.Body)
	}
	m := decode(t, e.get("/api/health", ""))
	if m["version"] != "test" || m["collector"] == nil || m["budget"] == nil {
		t.Fatalf("local health %v", m)
	}
}

func TestSearchAndGrid(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/api/search?q=%EC%84%9C%EA%B5%90", "/api/regions?q=%EC%84%9C%EA%B5%90&limit=5"} {
		m := decode(t, e.get(p, "203.0.113.9"))
		items := m["items"].([]any)
		if len(items) == 0 || items[0].(map[string]any)["code"] != "1144066000" {
			t.Fatalf("%s: %v", p, m)
		}
	}
	if w := e.get("/api/search?q=x&limit=0", "203.0.113.9"); w.Code != 400 {
		t.Fatalf("limit 0: %d", w.Code)
	}
	m := decode(t, e.get("/api/grid?lat=37.5497&lon=126.9230", "203.0.113.9"))
	if m["nx"] != 59.0 || m["ny"] != 126.0 || m["outside"] != false || m["region"].(map[string]any)["name"] != "서교동" {
		t.Fatalf("grid %v", m)
	}
	m = decode(t, e.get("/api/grid?lat=0&lon=0", "203.0.113.9"))
	if m["outside"] != true || m["nx"] != nil {
		t.Fatalf("outside %v", m)
	}
	if w := e.get("/api/grid?lat=abc&lon=1", "203.0.113.9"); w.Code != 400 {
		t.Fatalf("bad lat %d", w.Code)
	}
	// 위경도는 로그에 남기지 않음
	if strings.Contains(e.logs.String(), "37.5497") || strings.Contains(e.logs.String(), "lat=") {
		t.Fatal("query string logged")
	}
	if !strings.Contains(e.logs.String(), "client_ip=203.0.113.9") {
		t.Fatal("client ip not logged")
	}
}

func TestTimelineNewGrid(t *testing.T) {
	e := newEnv(t)
	w := e.get("/api/timeline?nx=60&ny=127", "203.0.113.9")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	m := decode(t, w)
	place := m["place"].(map[string]any)
	if _, ok := place["archived"]; ok || place["nx"] != 60.0 || place["name"] == "" {
		t.Fatalf("place %v", place)
	}
	if _, ok := place["fav_id"]; ok {
		t.Fatal("fav_id")
	}
	if len(m["slots"].([]any)) == 0 || len(m["daily"].([]any)) == 0 {
		t.Fatal("empty timeline")
	}
	for _, r := range e.kma.Requests() {
		if r.NX != 60 {
			t.Fatalf("unexpected request %+v", r)
		}
	}
	e.col.WaitBackground()
	// 다시: 호출 없이 complete, ETag로 304
	e.kma.Reset()
	w = e.get("/api/timeline?nx=60&ny=127", "203.0.113.9")
	m = decode(t, w)
	if m["complete"] != true || len(e.kma.Requests()) != 0 {
		t.Fatalf("second: complete=%v reqs=%d", m["complete"], len(e.kma.Requests()))
	}
	etag := w.Header().Get("ETag")
	if etag == "" || w.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Fatalf("headers %v", w.Header())
	}
	if w := e.get("/api/timeline?nx=60&ny=127", "203.0.113.9", "If-None-Match", etag); w.Code != 304 {
		t.Fatalf("304: %d", w.Code)
	}
	// 같은 시각이라도 now가 바뀌면 같은 ETag (now는 해시에서 뺌)
	e.now = e.now.Add(time.Minute)
	if w := e.get("/api/timeline?nx=60&ny=127", "203.0.113.9"); w.Header().Get("ETag") != etag {
		t.Fatal("etag changed by now only")
	}
}

func TestTimelineCollectedGridAndErrors(t *testing.T) {
	e := newEnv(t)
	// 수집 대상 격자는 즉시 조회하지 않음. 저장된 단기예보가 없으면 503
	w := e.get("/api/timeline?nx=59&ny=126&region=1144066000", "203.0.113.9")
	if w.Code != 503 || len(e.kma.Requests()) != 0 || !strings.Contains(w.Body.String(), "upstream_unavailable") {
		t.Fatalf("%d %s reqs=%d", w.Code, w.Body, len(e.kma.Requests()))
	}
	for _, p := range []string{
		"/api/timeline", "/api/timeline?nx=0&ny=1", "/api/timeline?nx=60&ny=127&region=1144066000",
		"/api/timeline?region=999", "/api/timeline?nx=a&ny=1",
	} {
		if w := e.get(p, "203.0.113.9"); w.Code != 400 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	// region만 주면 그 격자
	e.col.Ensure(context.Background(), 60, 127)
	if w := e.get("/api/timeline?region=1111051500", "203.0.113.9"); w.Code != 200 || decode(t, w)["place"].(map[string]any)["name"] != "청운효자동" {
		t.Fatalf("region only: %d", w.Code)
	}
	if w := e.get("/nope", "203.0.113.9"); w.Code != 404 {
		t.Fatal("404")
	}
	if w := e.get("/api/nope", "203.0.113.9"); w.Code != 404 || !strings.Contains(w.Body.String(), "not_found") {
		t.Fatal("api 404")
	}
}

func TestNewGridRateLimit(t *testing.T) {
	e := newEnv(t)
	ok := 0
	for i := 0; i < 12; i++ {
		w := e.get(fmt.Sprintf("/api/timeline?nx=%d&ny=100", 10+i), "198.51.100.7")
		switch w.Code {
		case 200:
			ok++
		case 429:
			if w.Header().Get("Retry-After") == "" {
				t.Fatal("no Retry-After")
			}
		default:
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	if ok != 10 {
		t.Fatalf("new grids allowed %d, want 10/hour", ok)
	}
	// 이미 본 격자는 새 격자 한도와 관계없음, 다른 IP는 따로
	if w := e.get("/api/timeline?nx=10&ny=100", "198.51.100.7"); w.Code != 200 {
		t.Fatalf("seen grid %d", w.Code)
	}
	if w := e.get("/api/timeline?nx=30&ny=100", "2001:db8::1"); w.Code != 200 {
		t.Fatalf("other ip %d", w.Code)
	}
}

func TestTimelineRateLimit(t *testing.T) {
	e := newEnv(t)
	n429 := 0
	for i := 0; i < 25; i++ {
		if w := e.get("/api/timeline?nx=0&ny=0", "192.0.2.50"); w.Code == 429 {
			n429++
		}
	}
	if n429 != 5 {
		t.Fatalf("429s %d (burst 20)", n429)
	}
	// 서버 안(local) 요청은 제한하지 않음
	for i := 0; i < 25; i++ {
		if w := e.get("/api/timeline?nx=0&ny=0", ""); w.Code == 429 {
			t.Fatal("local limited")
		}
	}
}

func TestTimelineConcurrentNewGrid(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e.get("/api/timeline?nx=70&ny=120", fmt.Sprintf("203.0.113.%d", i+1))
		}(i)
	}
	wg.Wait()
	e.col.WaitBackground()
	seen := map[string]int{}
	for _, r := range e.kma.Requests() {
		seen[fmt.Sprintf("%s/%d", r.Op, r.Base)]++
	}
	for k, n := range seen {
		if n != 1 {
			t.Fatalf("%s requested %d times", k, n)
		}
	}
	if len(seen) != 4 {
		t.Fatalf("keys %v", seen)
	}
}

func TestNoRealKMA(t *testing.T) {
	// 테스트 환경이 실제 주소를 쓰지 않는지 확인
	e := newEnv(t)
	if strings.Contains(e.kma.URL, "data.go.kr") {
		t.Fatal("real endpoint")
	}
	_ = io.Discard
}
