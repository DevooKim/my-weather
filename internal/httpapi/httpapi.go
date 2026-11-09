// Package httpapi는 공개 HTTP API다(설계 §2). 이번 단계는 인증 없이 읽기 전용 엔드포인트만 둔다.
//
//	GET /healthz          컨테이너 헬스체크 (DB SELECT 1)
//	GET /api/health       운영 상태. 상세는 서버 안(loopback, 전달 헤더 없음)에서만, 그 밖에는 {"ok":true}
//	GET /api/timeline     타임라인 (§3). 수집 대상이 아닌 격자는 즉시 조회(§5.6)
//	GET /api/search       지역 검색 (§2.3, /api/regions도 같은 처리)
//	GET /api/grid         위경도 → 격자 (§2.4)
//
// 응답에 수집 대상 여부(archived)는 넣지 않는다(Q14).
package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"myweather/internal/budget"
	"myweather/internal/collector"
	"myweather/internal/grid"
	"myweather/internal/kma"
	"myweather/internal/kst"
	"myweather/internal/ratelimit"
	"myweather/internal/store"
	"myweather/internal/timeline"
)

// Options는 서버 의존성이다.
type Options struct {
	DB        *store.DB
	DBPath    string // WAL 크기 보고용 (비워도 됨)
	Collector *collector.Collector
	Budget    *budget.Budget
	Regions   *grid.Table
	Version   string
	IP        *ratelimit.ClientIP
	Now       func() time.Time // nil이면 kst.Now
	Log       *slog.Logger
}

// Server는 HTTP 핸들러 묶음이다.
type Server struct {
	o       Options
	log     *slog.Logger
	started time.Time

	limTimeline *ratelimit.Limiter // GET /api/timeline: 분당 60, 순간 20
	limLookup   *ratelimit.Limiter // /api/search, /api/grid: 분당 120
	limOther    *ratelimit.Limiter // 그 외 /api/*: 분당 120
	limNewHour  *ratelimit.Limiter // KMA를 부르게 되는 새 격자: 시간당 10
	limNewDay   *ratelimit.Limiter // 하루 30
}

// New는 서버를 만든다.
func New(o Options) *Server {
	if o.Now == nil {
		o.Now = kst.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.IP == nil {
		o.IP = &ratelimit.ClientIP{TrustHeader: "X-Forwarded-For", Log: o.Log}
	}
	if o.Regions == nil {
		o.Regions = grid.MustLoad()
	}
	return &Server{
		o: o, log: o.Log, started: o.Now(),
		limTimeline: ratelimit.New(60, time.Minute, 20, nil),
		limLookup:   ratelimit.New(120, time.Minute, 120, nil),
		limOther:    ratelimit.New(120, time.Minute, 120, nil),
		limNewHour:  ratelimit.New(10, time.Hour, 10, nil),
		limNewDay:   ratelimit.New(30, 24*time.Hour, 30, nil),
	}
}

type ctxKey struct{}

type reqInfo struct {
	ip, key string
}

func info(r *http.Request) reqInfo {
	if v, ok := r.Context().Value(ctxKey{}).(reqInfo); ok {
		return v
	}
	return reqInfo{}
}

// Handler는 라우터에 미들웨어를 씌운 핸들러다.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /api/health", s.limited(s.limOther, s.health))
	mux.HandleFunc("GET /api/timeline", s.limited(s.limTimeline, s.timeline))
	mux.HandleFunc("GET /api/search", s.limited(s.limLookup, s.search))
	mux.HandleFunc("GET /api/regions", s.limited(s.limLookup, s.search))
	mux.HandleFunc("GET /api/grid", s.limited(s.limLookup, s.gridLookup))
	mux.HandleFunc("/api/", s.limited(s.limOther, func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not_found", "없는 API예요.", 0)
	}))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not_found", "없는 경로예요.", 0)
	})
	return s.middleware(mux)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// middleware: 클라이언트 IP 판정, 패닉 복구, 요청 로그(쿼리 문자열은 남기지 않음: §2.4 위경도).
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		ip, key := s.o.IP.Resolve(r)
		r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, reqInfo{ip, key}))
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			if p := recover(); p != nil {
				s.log.Error("http: panic", "path", r.URL.Path, "panic", fmt.Sprint(p))
				if sw.status == 0 {
					writeErr(sw, http.StatusInternalServerError, "internal", "서버 오류가 났어요.", 0)
				}
			}
			lvl := slog.LevelInfo
			if r.URL.Path == "/healthz" {
				lvl = slog.LevelDebug
			}
			s.log.Log(r.Context(), lvl, "http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
				"ms", time.Since(t0).Milliseconds(), "client_ip", ip)
		}()
		next.ServeHTTP(sw, r)
	})
}

// limited는 IP별 요청 제한을 씌운다. 서버 안(local) 요청은 제한하지 않는다.
func (s *Server) limited(l *ratelimit.Limiter, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if k := info(r).key; k != ratelimit.KeyLocal {
			if ok, wait := l.Allow(k); !ok {
				writeErr(w, http.StatusTooManyRequests, "rate_limited", "요청이 너무 많아요. 잠시 뒤 다시 시도해 주세요.", wait)
				return
			}
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string, retry time.Duration) {
	e := map[string]any{"code": code, "message": msg}
	if retry > 0 {
		sec := int(math.Ceil(retry.Seconds()))
		e["retry_after_s"] = sec
		w.Header().Set("Retry-After", strconv.Itoa(sec))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]any{"error": e})
}

// --- /healthz, /api/health ---

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	w.Header().Set("Cache-Control", "no-store")
	if err := s.o.DB.Ping(ctx); err != nil {
		s.log.Error("healthz: db", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type opHealth struct {
	LatestBase   *time.Time `json:"latest_base"`
	GridsOK      int        `json:"grids_ok"`
	GridsWaiting int        `json:"grids_waiting"`
	Waiting      []waitItem `json:"waiting,omitempty"`
}

type waitItem struct {
	NX       int       `json:"nx"`
	NY       int       `json:"ny"`
	Base     time.Time `json:"base"`
	Tries    int       `json:"tries"`
	LastCode string    `json:"last_code,omitempty"`
	GiveUp   bool      `json:"give_up,omitempty"`
}

// health: 인증이 없으므로 상세(수집 격자 등)는 서버 안에서 온 요청(KeyLocal)에만 준다(Q14).
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if info(r).key != ratelimit.KeyLocal {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	ctx := r.Context()
	now := s.o.Now()
	out := map[string]any{"ok": true, "now": now, "version": s.o.Version, "uptime_s": int(now.Sub(s.started).Seconds())}
	col := map[string]*opHealth{}
	var grids [][2]int
	if s.o.Collector != nil {
		grids = s.o.Collector.Grids()
		for _, op := range kma.Ops {
			h := &opHealth{}
			exp := kst.ToInt(op.Kind().Latest(now))
			for _, g := range grids {
				var b int64
				var err error
				if op == kma.OpNcst {
					b, err = s.o.DB.LatestObs(ctx, g[0], g[1])
				} else {
					b, err = s.o.DB.LatestFcstBase(ctx, op.Short(), g[0], g[1])
				}
				if err != nil {
					writeErr(w, http.StatusInternalServerError, "internal", "상태를 읽지 못했어요.", 0)
					return
				}
				if b > 0 && (h.LatestBase == nil || kst.FromInt(b).After(*h.LatestBase)) {
					t := kst.FromInt(b)
					h.LatestBase = &t
				}
				if b >= exp {
					h.GridsOK++
				}
			}
			col[string(op)] = h
		}
		for _, st := range s.o.Collector.Waiting() {
			h := col[st.Op]
			if h == nil {
				continue
			}
			h.GridsWaiting++
			h.Waiting = append(h.Waiting, waitItem{st.NX, st.NY, kst.FromInt(st.Base), st.Tries, st.LastCode, st.GiveUp})
		}
	}
	out["collector"] = col
	out["grids"] = grids
	if s.o.Budget != nil {
		snap := s.o.Budget.Snapshot()
		out["budget"] = map[string]any{"date": strconv.FormatInt(snap.Day, 10), "limit_per_op": snap.Limit, "by_op": snap.ByOp, "levels": snap.Levels}
	}
	dbInfo := map[string]any{}
	if n, err := s.o.DB.SizeBytes(ctx); err == nil {
		dbInfo["size_mb"] = math.Round(float64(n)/1e5) / 10
	}
	if s.o.DBPath != "" {
		if st, err := os.Stat(s.o.DBPath + "-wal"); err == nil {
			dbInfo["wal_mb"] = math.Round(float64(st.Size())/1e5) / 10
		}
	}
	out["db"] = dbInfo
	writeJSON(w, http.StatusOK, out)
}

// --- /api/search ---

type regionJSON struct {
	Code string   `json:"code"`
	Name string   `json:"name"`
	Full string   `json:"full"`
	NX   int      `json:"nx"`
	NY   int      `json:"ny"`
	Lat  *float64 `json:"lat"`
	Lon  *float64 `json:"lon"`
}

func toRegionJSON(r *grid.Region) regionJSON {
	out := regionJSON{Code: r.Code, Name: r.Name(), Full: r.Full(), NX: r.NX, NY: r.NY}
	if r.HasPos {
		lat, lon := r.Lat, r.Lon
		out.Lat, out.Lon = &lat, &lon
	}
	return out
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) > 50 {
		writeErr(w, http.StatusBadRequest, "bad_request", "검색어가 너무 길어요.", 0)
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeErr(w, http.StatusBadRequest, "bad_request", "limit은 1 이상의 정수예요.", 0)
			return
		}
		limit = min(n, 50)
	}
	items := []regionJSON{}
	for _, reg := range s.o.Regions.Search(q, limit) {
		items = append(items, toRegionJSON(reg))
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// --- /api/grid ---

func parseCoord(v string, lo, hi float64) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || math.IsNaN(f) || f < lo || f > hi {
		return 0, false
	}
	return f, true
}

func (s *Server) gridLookup(w http.ResponseWriter, r *http.Request) {
	lat, ok1 := parseCoord(r.URL.Query().Get("lat"), -90, 90)
	lon, ok2 := parseCoord(r.URL.Query().Get("lon"), -180, 180)
	if !ok1 || !ok2 {
		writeErr(w, http.StatusBadRequest, "bad_request", "lat·lon이 올바르지 않아요.", 0)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	nx, ny, ok := grid.ToGrid(lat, lon)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"nx": nil, "ny": nil, "region": nil, "outside": true})
		return
	}
	// 지명은 같은 격자 안에서 가장 가까운 동(타임라인 요청의 region이 격자와 맞도록)
	var region any
	if reg, d := s.o.Regions.NearestInGrid(nx, ny, lat, lon); reg != nil {
		region = map[string]any{"code": reg.Code, "name": reg.Name(), "full": reg.Full(), "distance_km": math.Round(d*10) / 10}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nx": nx, "ny": ny, "region": region, "outside": false})
}

// --- /api/timeline ---

// place는 요청의 nx·ny·region으로 장소를 정한다.
func (s *Server) place(r *http.Request) (timeline.Place, error) {
	q := r.URL.Query()
	code := strings.TrimSpace(q.Get("region"))
	var reg *grid.Region
	if code != "" {
		if reg = s.o.Regions.ByCode(code); reg == nil {
			return timeline.Place{}, errors.New("없는 행정구역 코드예요.")
		}
	}
	nxs, nys := q.Get("nx"), q.Get("ny")
	var nx, ny int
	switch {
	case nxs == "" && nys == "" && reg != nil:
		nx, ny = reg.NX, reg.NY
	default:
		var err1, err2 error
		nx, err1 = strconv.Atoi(nxs)
		ny, err2 = strconv.Atoi(nys)
		if err1 != nil || err2 != nil || !grid.Valid(nx, ny) {
			return timeline.Place{}, errors.New("nx(1~149)·ny(1~253)가 올바르지 않아요.")
		}
		if reg != nil && (reg.NX != nx || reg.NY != ny) {
			return timeline.Place{}, errors.New("region이 이 격자에 있지 않아요.")
		}
	}
	if reg == nil {
		reg = s.o.Regions.ForGrid(nx, ny)
	}
	p := timeline.Place{NX: nx, NY: ny}
	p.Lat, p.Lon = grid.ToLatLon(nx, ny)
	if reg != nil {
		p.Name, p.Full, p.Region = reg.Name(), reg.Full(), reg.Code
		if reg.HasPos {
			p.Lat, p.Lon = reg.Lat, reg.Lon
		}
	}
	p.Lat, p.Lon = math.Round(p.Lat*1e4)/1e4, math.Round(p.Lon*1e4)/1e4
	return p, nil
}

func (s *Server) timeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	place, err := s.place(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), 0)
		return
	}
	ens := collector.Ensured{Complete: true}
	if c := s.o.Collector; c != nil && !c.IsCollected(place.NX, place.NY) {
		isNew, err := c.IsNewGrid(ctx, place.NX, place.NY)
		if err != nil {
			s.internal(w, "timeline: grid_seen", err)
			return
		}
		if k := info(r).key; isNew && k != ratelimit.KeyLocal {
			cost := 1
			if s.o.Budget != nil && !s.o.Budget.Allows(string(kma.OpVilage), budget.OnDemand3) {
				cost = 2 // 예산 tight 이상: 새 격자 IP 한도 절반 (§6.3)
			}
			if ok, wait := ratelimit.AllowAll(k, cost, s.limNewHour, s.limNewDay); !ok {
				writeErr(w, http.StatusTooManyRequests, "rate_limited", "새 장소 조회가 너무 많아요. 잠시 뒤 다시 시도해 주세요.", wait)
				return
			}
		}
		if ens, err = c.Ensure(ctx, place.NX, place.NY); err != nil {
			if ctx.Err() != nil {
				return
			}
			s.internal(w, "timeline: ensure", err)
			return
		}
	}
	in, err := timeline.Load(ctx, s.o.DB, place, s.o.Now())
	if err != nil {
		s.internal(w, "timeline: load", err)
		return
	}
	if len(in.Vilage) == 0 {
		// 쓸 단기예보가 하나도 없음: 예산 차단이면 오래, 아직 받는 중이면 짧게 다시 시도
		retry := time.Minute
		if !ens.Complete && ens.RetryAfter > 0 {
			retry = ens.RetryAfter
		}
		writeErr(w, http.StatusServiceUnavailable, "upstream_unavailable", "기상청 자료를 아직 받지 못했어요. 잠시 뒤 다시 시도해 주세요.", retry)
		return
	}
	tl := timeline.Build(in)
	tl.Complete = ens.Complete
	if !ens.Complete {
		sec := int(math.Ceil(ens.RetryAfter.Seconds()))
		tl.RetryAfterS = &sec
		tl.Warnings = append(tl.Warnings, "obs_backfill_pending")
	}
	if ens.Degraded {
		tl.Warnings = append(tl.Warnings, "budget_degraded")
	}

	// ETag·data_version: now를 뺀 응답 내용의 해시. 뒤에서 채운 과거 실황도 반영된다.
	now := tl.Now
	tl.Now = time.Time{}
	b, err := json.Marshal(tl)
	if err != nil {
		s.internal(w, "timeline: marshal", err)
		return
	}
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:8])
	tl.Now = now
	tl.DataVersion += ":" + h
	etag := `"` + h + `"`
	w.Header().Set("ETag", etag)
	if ens.Complete {
		w.Header().Set("Cache-Control", "public, max-age=60")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if match := r.Header.Get("If-None-Match"); match != "" && etagMatch(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, http.StatusOK, tl)
}

func etagMatch(header, etag string) bool {
	for _, v := range strings.Split(header, ",") {
		v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "W/"))
		if v == etag || v == "*" {
			return true
		}
	}
	return false
}

func (s *Server) internal(w http.ResponseWriter, what string, err error) {
	s.log.Error(what, "err", err)
	writeErr(w, http.StatusInternalServerError, "internal", "서버 오류가 났어요.", 0)
}
