// Package httpapi는 HTTP API다(설계 §2).
//
//	GET /healthz          컨테이너 헬스체크 (DB SELECT 1)
//	GET /api/health       운영 상태. 상세는 서버 안(loopback, 전달 헤더 없음) 또는 관리자 세션에만, 그 밖에는 {"ok":true}
//	GET /api/timeline     타임라인 (§3). 수집 대상이 아닌 격자는 즉시 조회(§5.6). ?fav=id는 관리자만
//	GET /api/now          지금 날씨 묶음 (§2.2, KMA를 부르지 않음)
//	GET /api/search       지역 검색 (§2.3, /api/regions도 같은 처리)
//	GET /api/grid         위경도 → 격자 (§2.4)
//	/api/auth/*           패스키 등록·로그인·로그아웃·상태·패스키 관리 (§2.8, admin.go)
//	/api/favorites*       즐겨찾기 (§2.5, 관리자)
//	/api/alerts/settings  알림 설정 (§2.6, 관리자)
//	/api/push/*           Web Push 구독·시험 (§2.7)
//	GET /api/settings/info 설정 화면 정보 (공개 부분 + 관리자 부분)
//	그 밖의 /api/*는 JSON 404, /api/ 밖은 internal/webui (SPA)
//
// 상태를 바꾸는 /api/ 요청(POST·PUT·PATCH·DELETE)은 Origin이 MYWEATHER_ORIGIN과 같아야 한다(없거나 다르면 403).
// 비로그인 응답에 수집 대상 여부(archived)는 넣지 않는다(Q14).
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

	"myweather/internal/air"
	"myweather/internal/alert"
	"myweather/internal/auth"
	"myweather/internal/budget"
	"myweather/internal/collector"
	"myweather/internal/fog"
	"myweather/internal/grid"
	"myweather/internal/kma"
	"myweather/internal/kst"
	"myweather/internal/push"
	"myweather/internal/ratelimit"
	"myweather/internal/store"
	"myweather/internal/timeline"
	"myweather/internal/webui"
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

	Air   *air.Client  // nil이면 타임라인에 미세먼지(air)를 넣지 않는다
	Fog   *fog.Client  // nil이면 타임라인에 안개(fog)를 넣지 않는다
	Auth  *auth.Auth   // nil이면 인증·관리자 API는 503
	Push  *push.Sender // nil이면 푸시 API는 503
	Rules alert.Rules  // 알림 설정 화면에 보여 줄 기준값 (0값이면 alert.DefaultRules)
	Web   http.Handler // /api/ 밖 경로. nil이면 webui.Handler()
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
	limReg      *ratelimit.Limiter // 패스키 등록: 분당 5
	limLogin    *ratelimit.Limiter // 로그인: 분당 10
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
	if o.Rules == (alert.Rules{}) {
		o.Rules = alert.DefaultRules
	}
	if o.Web == nil {
		o.Web = webui.Handler()
	}
	return &Server{
		o: o, log: o.Log, started: o.Now(),
		limTimeline: ratelimit.New(60, time.Minute, 20, nil),
		limLookup:   ratelimit.New(120, time.Minute, 120, nil),
		limOther:    ratelimit.New(120, time.Minute, 120, nil),
		limNewHour:  ratelimit.New(10, time.Hour, 10, nil),
		limNewDay:   ratelimit.New(30, 24*time.Hour, 30, nil),
		limReg:      ratelimit.New(5, time.Minute, 5, nil),
		limLogin:    ratelimit.New(10, time.Minute, 10, nil),
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
	mux.HandleFunc("GET /api/now", s.limited(s.limLookup, s.nowBatch))

	// 인증 (§2.8)
	mux.HandleFunc("POST /api/auth/register/options", s.limited(s.limReg, s.registerOptions))
	mux.HandleFunc("POST /api/auth/register/verify", s.limited(s.limReg, s.registerVerify))
	mux.HandleFunc("POST /api/auth/login/options", s.limited(s.limLogin, s.loginOptions))
	mux.HandleFunc("POST /api/auth/login/verify", s.limited(s.limLogin, s.loginVerify))
	mux.HandleFunc("POST /api/auth/logout", s.limited(s.limOther, s.signedIn(s.logout)))
	mux.HandleFunc("GET /api/auth/me", s.limited(s.limOther, s.me))
	mux.HandleFunc("POST /api/auth/enroll-tokens", s.limited(s.limOther, s.admin(s.issueEnrollToken)))
	mux.HandleFunc("GET /api/auth/passkeys", s.limited(s.limOther, s.signedIn(s.passkeys)))
	mux.HandleFunc("DELETE /api/auth/passkeys/{id}", s.limited(s.limOther, s.signedIn(s.deletePasskey)))

	// 즐겨찾기: 로그인한 사용자 자신의 목록
	mux.HandleFunc("GET /api/favorites", s.limited(s.limOther, s.signedIn(s.favoritesGet)))
	mux.HandleFunc("POST /api/favorites", s.limited(s.limOther, s.signedIn(s.favoritesAdd)))
	mux.HandleFunc("POST /api/favorites/merge", s.limited(s.limOther, s.signedIn(s.favoritesMerge)))
	mux.HandleFunc("PUT /api/favorites/order", s.limited(s.limOther, s.signedIn(s.favoritesOrder)))
	mux.HandleFunc("DELETE /api/favorites/{id}", s.limited(s.limOther, s.signedIn(s.favoritesDelete)))
	mux.HandleFunc("PATCH /api/favorites/{id}", s.limited(s.limOther, s.signedIn(s.favoritesRename)))

	// 알림 설정
	mux.HandleFunc("GET /api/alerts/settings", s.limited(s.limOther, s.signedIn(s.alertSettingsGet)))
	mux.HandleFunc("PATCH /api/alerts/settings", s.limited(s.limOther, s.signedIn(s.alertSettingsPatch)))

	// 푸시
	mux.HandleFunc("GET /api/push/vapid-public-key", s.limited(s.limOther, s.vapidKey))
	mux.HandleFunc("POST /api/push/subscriptions", s.limited(s.limOther, s.signedIn(s.pushSubscribe)))
	mux.HandleFunc("DELETE /api/push/subscriptions", s.limited(s.limOther, s.signedIn(s.pushUnsubscribe)))
	mux.HandleFunc("POST /api/push/test", s.limited(s.limOther, s.signedIn(s.pushTest)))

	// 설정 화면
	mux.HandleFunc("GET /api/settings/info", s.limited(s.limOther, s.settingsInfo))

	mux.HandleFunc("/api/", s.limited(s.limOther, func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not_found", "없는 API예요.", 0)
	}))
	mux.Handle("/", s.o.Web)
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
		r.Body = http.MaxBytesReader(sw, r.Body, 1<<20)
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
		if !s.checkUnsafe(sw, r) {
			return
		}
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

// health: 상세(수집 격자 등)는 서버 안에서 온 요청(KeyLocal)이나 관리자 세션에만 준다(Q14).
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if !s.isAdmin(w, r) {
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
	pushInfo := map[string]any{"enabled": s.o.Push != nil}
	if n, err := s.o.DB.PushSubCount(ctx); err == nil {
		pushInfo["subscriptions"] = n
	}
	if v, err := s.o.DB.LastPushOK(ctx); err == nil {
		pushInfo["last_sent_at"] = ts(v)
	}
	out["push"] = pushInfo
	out["auth"] = map[string]any{"enabled": s.o.Auth != nil}
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

// airNow는 장소의 지금 미세먼지다. 측정소를 처음 찾거나 새 자료를 받을 때만 에어코리아를 부르고,
// 늦어도 타임라인 응답을 오래 붙잡지 않게 3초에서 끊는다.
func (s *Server) airNow(ctx context.Context, p timeline.Place) *air.Now {
	if s.o.Air == nil {
		return nil
	}
	key := p.Region
	if key == "" {
		key = fmt.Sprintf("g%d,%d", p.NX, p.NY)
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return s.o.Air.Get(ctx, air.Place{Code: key, Lat: p.Lat, Lon: p.Lon})
}

// favPlace는 ?fav=id(관리자)의 장소다. 이름은 즐겨찾기 이름.
func (s *Server) favPlace(w http.ResponseWriter, r *http.Request) (timeline.Place, bool) {
	sess, ok := s.session(w, r)
	if !ok {
		if s.o.Auth == nil {
			notConfigured(w, "로그인")
		} else {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "로그인이 필요해요.", 0)
		}
		return timeline.Place{}, false
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("fav"), 10, 64)
	var f store.Favorite
	if err == nil {
		f, err = s.o.DB.Favorite(r.Context(), sess.UserID, id)
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || id <= 0 {
			writeErr(w, http.StatusNotFound, "not_found", "없는 즐겨찾기예요.", 0)
		} else {
			s.internal(w, "timeline: favorite", err)
		}
		return timeline.Place{}, false
	}
	p := timeline.Place{NX: f.NX, NY: f.NY, Name: f.Name, Region: f.Region}
	p.Lat, p.Lon = grid.ToLatLon(f.NX, f.NY)
	reg := s.o.Regions.ByCode(f.Region)
	if reg == nil {
		reg = s.o.Regions.ForGrid(f.NX, f.NY)
	}
	if reg != nil {
		p.Full = reg.Full()
		if reg.HasPos {
			p.Lat, p.Lon = reg.Lat, reg.Lon
		}
	}
	p.Lat, p.Lon = math.Round(p.Lat*1e4)/1e4, math.Round(p.Lon*1e4)/1e4
	return p, true
}

func (s *Server) timeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var place timeline.Place
	isFav := r.URL.Query().Has("fav")
	if isFav {
		var ok bool
		if place, ok = s.favPlace(w, r); !ok {
			return
		}
	} else {
		var err error
		if place, err = s.place(r); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), 0)
			return
		}
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
		} else if c := s.o.Collector; c != nil && c.IsCollected(place.NX, place.NY) {
			retry = 10 * time.Second // 방금 즐겨찾기에 넣은 격자: 수집 루프가 곧 받는다(KMA 호출 없이 DB만 다시 읽음)
		}
		writeErr(w, http.StatusServiceUnavailable, "upstream_unavailable", "기상청 자료를 아직 받지 못했어요. 잠시 뒤 다시 시도해 주세요.", retry)
		return
	}
	tl := timeline.Build(in)
	tl.Air = s.airNow(ctx, place)
	if s.o.Fog != nil {
		fctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		tl.Fog = s.o.Fog.Get(fctx, place.Lat, place.Lon)
		cancel()
	}
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
	if isFav {
		w.Header().Set("Cache-Control", "private, no-cache")
	} else if ens.Complete {
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
