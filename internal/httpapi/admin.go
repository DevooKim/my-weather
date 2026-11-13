package httpapi

// 3단계 엔드포인트: 인증(§2.8), 즐겨찾기(§2.5), 알림 설정(§2.6), 푸시 구독(§2.7), 지금 날씨 묶음(§2.2), 설정 화면 정보.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"myweather/internal/auth"
	"myweather/internal/grid"
	"myweather/internal/kma"
	"myweather/internal/kst"
	"myweather/internal/push"
	"myweather/internal/ratelimit"
	"myweather/internal/store"
	"myweather/internal/timeline"
)

type sessKey struct{}

const maxBody = 64 << 10

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "요청 본문이 올바르지 않아요.", 0)
		return false
	}
	if dec.More() {
		writeErr(w, http.StatusBadRequest, "bad_request", "요청 본문이 올바르지 않아요.", 0)
		return false
	}
	return true
}

func notConfigured(w http.ResponseWriter, what string) {
	writeErr(w, http.StatusServiceUnavailable, "upstream_unavailable", what+" 설정이 없어요.", 0)
}

// session은 요청의 관리자 세션이다(admin 미들웨어를 거쳤거나, 선택 인증 핸들러에서 직접 확인).
func (s *Server) session(w http.ResponseWriter, r *http.Request) (store.Session, bool) {
	if v, ok := r.Context().Value(sessKey{}).(store.Session); ok {
		return v, true
	}
	if s.o.Auth == nil {
		return store.Session{}, false
	}
	return s.o.Auth.Session(w, r)
}

// signedIn은 로그인한 사용자 세션이 필요한 핸들러를 감싼다(없으면 401). 인증 설정이 없으면 503.
func (s *Server) signedIn(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.o.Auth == nil {
			notConfigured(w, "로그인")
			return
		}
		sess, ok := s.o.Auth.Session(w, r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "로그인이 필요해요.", 0)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		h(w, r.WithContext(context.WithValue(r.Context(), sessKey{}, sess)))
	}
}

// admin은 관리자 역할이 필요한 핸들러를 감싼다.
func (s *Server) admin(h http.HandlerFunc) http.HandlerFunc {
	return s.signedIn(func(w http.ResponseWriter, r *http.Request) {
		sess, _ := s.session(w, r)
		if sess.Role != store.RoleAdmin {
			writeErr(w, http.StatusForbidden, "forbidden", "관리자만 할 수 있어요.", 0)
			return
		}
		h(w, r)
	})
}

func (s *Server) uid(w http.ResponseWriter, r *http.Request) int64 {
	sess, _ := s.session(w, r)
	return sess.UserID
}

// checkUnsafe: 상태를 바꾸는 /api/ 요청은 Origin이 설정값과 같아야 한다(없거나 다르면 403). 인증 설정이 없으면 503.
func (s *Server) checkUnsafe(w http.ResponseWriter, r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	if s.o.Auth == nil {
		notConfigured(w, "로그인")
		return false
	}
	if !s.o.Auth.CheckOrigin(r) {
		s.log.Warn("http: origin rejected", "path", r.URL.Path, "origin", r.Header.Get("Origin"), "client_ip", info(r).ip)
		writeErr(w, http.StatusForbidden, "forbidden", "허용하지 않은 출처의 요청이에요.", 0)
		return false
	}
	return true
}

func ts(v int64) *time.Time {
	if v == 0 {
		return nil
	}
	t := time.Unix(v, 0).In(kst.Loc)
	return &t
}

// --- 인증 ---

type passkeyJSON struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	CreatedAt  *time.Time `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	Current    bool       `json:"current"`
}

func toPasskeyJSON(p store.Passkey, cur []byte) passkeyJSON {
	return passkeyJSON{ID: auth.PasskeyID(p.CredID), Label: p.Label, CreatedAt: ts(p.CreatedAt), LastUsedAt: ts(p.LastUsedAt),
		Current: cur != nil && string(cur) == string(p.CredID)}
}

// authErr는 auth 패키지의 알려진 오류를 응답으로 쓴다. 알려진 오류가 아니면 false.
func authErr(w http.ResponseWriter, err error, verifyStatus int) bool {
	switch {
	case errors.Is(err, auth.ErrBadToken):
		writeErr(w, http.StatusForbidden, "forbidden", "등록 코드가 맞지 않거나 만료됐어요.", 0)
	case errors.Is(err, auth.ErrChallenge):
		writeErr(w, http.StatusBadRequest, "bad_request", "시간이 지났어요. 처음부터 다시 시도해 주세요.", 0)
	case errors.Is(err, auth.ErrDuplicate):
		writeErr(w, http.StatusConflict, "conflict", "이미 등록된 패스키예요.", 0)
	case errors.Is(err, auth.ErrBusy):
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "요청이 너무 많아요. 잠시 뒤 다시 시도해 주세요.", 30*time.Second)
	case errors.Is(err, auth.ErrVerify):
		code := "bad_request"
		msg := "패스키를 확인하지 못했어요."
		if verifyStatus == http.StatusUnauthorized {
			code = "unauthorized"
		}
		writeErr(w, verifyStatus, code, msg, 0)
	default:
		return false
	}
	return true
}

func (s *Server) needAuth(w http.ResponseWriter) bool {
	if s.o.Auth == nil {
		notConfigured(w, "로그인")
		return false
	}
	return true
}

func (s *Server) registerOptions(w http.ResponseWriter, r *http.Request) {
	if !s.needAuth(w) {
		return
	}
	var req struct {
		Token string `json:"token"`
		Label string `json:"label"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = "패스키"
	}
	if utf8.RuneCountInString(label) > 40 || strings.TrimSpace(req.Token) == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "등록 코드와 기기 이름(40자 이하)을 확인해 주세요.", 0)
		return
	}
	cc, err := s.o.Auth.BeginRegistration(r.Context(), req.Token, label)
	if err != nil {
		s.handleAuthErr(w, "auth: register options", err, http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, cc)
}

func (s *Server) handleAuthErr(w http.ResponseWriter, what string, err error, verifyStatus int) {
	if !authErr(w, err, verifyStatus) {
		s.internal(w, what, err)
	}
}

func (s *Server) registerVerify(w http.ResponseWriter, r *http.Request) {
	if !s.needAuth(w) {
		return
	}
	var req struct {
		Token      string          `json:"token"`
		Credential json.RawMessage `json:"credential"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Credential) == 0 {
		writeErr(w, http.StatusBadRequest, "bad_request", "credential이 없어요.", 0)
		return
	}
	res, err := s.o.Auth.FinishRegistration(r.Context(), req.Token, req.Credential, r.UserAgent())
	if err != nil {
		s.handleAuthErr(w, "auth: register verify", err, http.StatusBadRequest)
		return
	}
	s.o.Auth.SetCookie(w, res.Cookie, res.ExpiresAt)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "passkey": toPasskeyJSON(res.Passkey, res.Passkey.CredID), "expires_at": res.ExpiresAt})
}

func (s *Server) loginOptions(w http.ResponseWriter, r *http.Request) {
	if !s.needAuth(w) {
		return
	}
	io.Copy(io.Discard, io.LimitReader(r.Body, maxBody))
	ca, err := s.o.Auth.BeginLogin(r.Context())
	if err != nil {
		s.handleAuthErr(w, "auth: login options", err, http.StatusUnauthorized)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, ca)
}

func (s *Server) loginVerify(w http.ResponseWriter, r *http.Request) {
	if !s.needAuth(w) {
		return
	}
	var req struct {
		Credential json.RawMessage `json:"credential"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Credential) == 0 {
		writeErr(w, http.StatusBadRequest, "bad_request", "credential이 없어요.", 0)
		return
	}
	res, err := s.o.Auth.FinishLogin(r.Context(), req.Credential, r.UserAgent())
	if err != nil {
		s.handleAuthErr(w, "auth: login verify", err, http.StatusUnauthorized)
		return
	}
	s.o.Auth.SetCookie(w, res.Cookie, res.ExpiresAt)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "expires_at": res.ExpiresAt})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.o.Auth.Logout(w, r); err != nil {
		s.internal(w, "auth: logout", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if _, ok := s.session(w, r); !ok {
		writeJSON(w, http.StatusOK, map[string]any{"admin": false})
		return
	}
	sess, _ := s.session(w, r)
	n, err := s.o.DB.PasskeyCount(r.Context(), sess.UserID)
	if err != nil {
		s.internal(w, "auth: me", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"admin": sess.Role == store.RoleAdmin, "role": sess.Role, "name": sess.Name, "passkeys": n})
}

func (s *Server) issueEnrollToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Role string `json:"role"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	role := strings.TrimSpace(req.Role)
	if !store.ValidRole(role) {
		writeErr(w, http.StatusBadRequest, "bad_request", "권한은 admin 또는 member예요.", 0)
		return
	}
	tok, exp, err := auth.IssueEnrollTokenAs(r.Context(), s.o.DB, auth.DefaultTokenTTL, false, role, "admin", s.o.Now())
	if err != nil {
		s.internal(w, "auth: enroll token", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"token": tok, "role": role, "expires_at": exp})
}

func (s *Server) passkeyList(ctx context.Context, userID int64, cur []byte) ([]passkeyJSON, error) {
	ps, err := s.o.DB.Passkeys(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := []passkeyJSON{}
	for _, p := range ps {
		out = append(out, toPasskeyJSON(p, cur))
	}
	return out, nil
}

func (s *Server) passkeys(w http.ResponseWriter, r *http.Request) {
	sess, _ := s.session(w, r)
	items, err := s.passkeyList(r.Context(), sess.UserID, sess.CredID)
	if err != nil {
		s.internal(w, "auth: passkeys", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) deletePasskey(w http.ResponseWriter, r *http.Request) {
	id, err := auth.ParsePasskeyID(r.PathValue("id"))
	if err != nil || len(id) == 0 {
		writeErr(w, http.StatusNotFound, "not_found", "없는 패스키예요.", 0)
		return
	}
	switch err := s.o.DB.DeletePasskey(r.Context(), s.uid(w, r), id); {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "없는 패스키예요.", 0)
	case errors.Is(err, store.ErrConflict):
		writeErr(w, http.StatusConflict, "conflict", "마지막 패스키는 지울 수 없어요.", 0)
	case err != nil:
		s.internal(w, "auth: delete passkey", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- 지금 날씨 (§2.2) ---

type nowJSON struct {
	NX         int        `json:"nx"`
	NY         int        `json:"ny"`
	Tmp        *float64   `json:"tmp"`
	Sky        *string    `json:"sky"`
	Pty        *string    `json:"pty"`
	ObservedAt *time.Time `json:"observed_at"`
	Src        *string    `json:"src"`
	Pending    bool       `json:"pending,omitempty"`
}

// nowOf는 저장된 데이터만으로 격자의 지금 값을 만든다(KMA를 부르지 않음). 데이터가 없으면 pending.
func (s *Server) nowOf(ctx context.Context, nx, ny int) (nowJSON, error) {
	out := nowJSON{NX: nx, NY: ny}
	p := timeline.Place{NX: nx, NY: ny}
	p.Lat, p.Lon = grid.ToLatLon(nx, ny)
	now := s.o.Now()
	in, err := timeline.Load(ctx, s.o.DB, p, now)
	if err != nil {
		return out, err
	}
	if len(in.Vilage) == 0 && len(in.Obs) == 0 {
		out.Pending = true
		return out, nil
	}
	tl := timeline.Build(in)
	for _, sl := range tl.Slots {
		if sl.Phase != "now" {
			continue
		}
		out.Tmp, out.Sky, out.Pty = sl.Tmp, sl.Sky, sl.Pty
		if src, ok := tl.Sources[sl.Src.Obs]; ok {
			k := src.Kind
			out.Src = &k
			at := sl.T
			if k == timeline.KindNcst {
				nh := now.Truncate(time.Hour)
				if len(in.Obs[kst.ToInt(nh)]) == 0 {
					at = nh.Add(-time.Hour)
				}
			}
			out.ObservedAt = &at
		}
		break
	}
	if out.Tmp == nil && out.Src == nil {
		out.Pending = true
	}
	return out, nil
}

// rawParam은 RawQuery에서 '&'로만 나눠 값을 찾는다. net/url은 ';'가 든 쿼리를 오류로 버리는데(Go 1.17+),
// §2.2 places는 격자를 ';'로 구분한다(인코딩한 %3B도 받음).
func rawParam(r *http.Request, name string) string {
	for _, kv := range strings.Split(r.URL.RawQuery, "&") {
		k, v, _ := strings.Cut(kv, "=")
		if k != name {
			continue
		}
		if u, err := url.QueryUnescape(v); err == nil {
			return u
		}
		return v
	}
	return ""
}

func (s *Server) nowBatch(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(rawParam(r, "places"))
	if raw == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "places가 필요해요 (예: 61,125;59,126).", 0)
		return
	}
	parts := strings.Split(raw, ";")
	if len(parts) > 20 {
		writeErr(w, http.StatusBadRequest, "bad_request", "places는 20개까지예요.", 0)
		return
	}
	items := []nowJSON{}
	for _, part := range parts {
		xy := strings.Split(strings.TrimSpace(part), ",")
		if len(xy) != 2 {
			writeErr(w, http.StatusBadRequest, "bad_request", "places 형식이 올바르지 않아요.", 0)
			return
		}
		nx, e1 := strconv.Atoi(strings.TrimSpace(xy[0]))
		ny, e2 := strconv.Atoi(strings.TrimSpace(xy[1]))
		if e1 != nil || e2 != nil || !grid.Valid(nx, ny) {
			writeErr(w, http.StatusBadRequest, "bad_request", "nx(1~149)·ny(1~253)가 올바르지 않아요.", 0)
			return
		}
		n, err := s.nowOf(r.Context(), nx, ny)
		if err != nil {
			s.internal(w, "now", err)
			return
		}
		items = append(items, n)
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// --- 즐겨찾기 (§2.5) ---

type alertsJSON struct {
	Soon bool `json:"soon"`
	Eve  bool `json:"eve"`
}

type favJSON struct {
	ID     int64      `json:"id"`
	Name   string     `json:"name"`
	Region *string    `json:"region"`
	NX     int        `json:"nx"`
	NY     int        `json:"ny"`
	Pos    int        `json:"pos"`
	Alerts alertsJSON `json:"alerts"`
	Now    *nowJSON   `json:"now,omitempty"`
}

func toFavJSON(f store.Favorite) favJSON {
	out := favJSON{ID: f.ID, Name: f.Name, NX: f.NX, NY: f.NY, Pos: f.Pos, Alerts: alertsJSON{f.Soon, f.Eve}}
	if f.Region != "" {
		r := f.Region
		out.Region = &r
	}
	return out
}

// favItems는 목록 응답 items다. now는 저장된 지금 값(없으면 null).
func (s *Server) favItems(ctx context.Context, fs []store.Favorite) ([]map[string]any, error) {
	cache := map[[2]int]*nowJSON{}
	out := []map[string]any{}
	for _, f := range fs {
		g := [2]int{f.NX, f.NY}
		n, ok := cache[g]
		if !ok {
			v, err := s.nowOf(ctx, f.NX, f.NY)
			if err != nil {
				return nil, err
			}
			if !v.Pending {
				n = &v
			}
			cache[g] = n
		}
		j := toFavJSON(f)
		m := map[string]any{"id": j.ID, "name": j.Name, "region": j.Region, "nx": j.NX, "ny": j.NY, "pos": j.Pos, "alerts": j.Alerts}
		if n != nil {
			m["now"] = map[string]any{"tmp": n.Tmp, "sky": n.Sky, "pty": n.Pty, "observed_at": n.ObservedAt, "src": n.Src}
		} else {
			m["now"] = nil
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *Server) writeFavList(w http.ResponseWriter, r *http.Request, status int, fs []store.Favorite, version int64, extra map[string]any) {
	items, err := s.favItems(r.Context(), fs)
	if err != nil {
		s.internal(w, "favorites: now", err)
		return
	}
	out := map[string]any{"items": items, "version": version}
	for k, v := range extra {
		out[k] = v
	}
	writeJSON(w, status, out)
}

// syncGrids는 즐겨찾기 격자를 수집 대상에 반영한다.
func (s *Server) syncGrids(ctx context.Context) {
	if s.o.Collector == nil {
		return
	}
	gs, err := s.o.DB.FavoriteGrids(ctx)
	if err != nil {
		s.log.Error("favorites: grids", "err", err)
		return
	}
	s.o.Collector.SetExtraGrids(gs)
}

type favReq struct {
	Name   string `json:"name"`
	Region string `json:"region"`
	NX     *int   `json:"nx"`
	NY     *int   `json:"ny"`
}

// favInput은 요청 장소를 확인한다. region이 있으면 표에 있어야 하고 격자가 맞아야 한다. 이름이 없으면 지명.
func (s *Server) favInput(q favReq) (store.FavInput, error) {
	in := store.FavInput{Name: strings.TrimSpace(q.Name), Region: strings.TrimSpace(q.Region)}
	var reg *grid.Region
	if in.Region != "" {
		if reg = s.o.Regions.ByCode(in.Region); reg == nil {
			return in, errors.New("없는 행정구역 코드예요.")
		}
	}
	switch {
	case q.NX != nil && q.NY != nil:
		in.NX, in.NY = *q.NX, *q.NY
		if !grid.Valid(in.NX, in.NY) {
			return in, errors.New("nx(1~149)·ny(1~253)가 올바르지 않아요.")
		}
		if reg != nil && (reg.NX != in.NX || reg.NY != in.NY) {
			return in, errors.New("region이 이 격자에 있지 않아요.")
		}
	case reg != nil && q.NX == nil && q.NY == nil:
		in.NX, in.NY = reg.NX, reg.NY
	default:
		return in, errors.New("nx·ny 또는 region이 필요해요.")
	}
	if in.Name == "" && reg != nil {
		in.Name = reg.Name()
	}
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 40 {
		return in, errors.New("이름은 1~40자예요.")
	}
	return in, nil
}

func (s *Server) favoritesGet(w http.ResponseWriter, r *http.Request) {
	fs, v, err := s.o.DB.Favorites(r.Context(), s.uid(w, r))
	if err != nil {
		s.internal(w, "favorites", err)
		return
	}
	s.writeFavList(w, r, http.StatusOK, fs, v, nil)
}

func (s *Server) favoritesAdd(w http.ResponseWriter, r *http.Request) {
	var q favReq
	if !readJSON(w, r, &q) {
		return
	}
	in, err := s.favInput(q)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error(), 0)
		return
	}
	f, created, v, err := s.o.DB.AddFavorite(r.Context(), s.uid(w, r), in, s.o.Now())
	if err != nil {
		s.internal(w, "favorites: add", err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		s.syncGrids(r.Context())
	}
	writeJSON(w, status, map[string]any{"item": toFavJSON(f), "version": v})
}

func (s *Server) favoritesMerge(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Items []favReq `json:"items"`
	}
	if !readJSON(w, r, &q) {
		return
	}
	if len(q.Items) > 50 {
		writeErr(w, http.StatusBadRequest, "bad_request", "한 번에 50개까지 합칠 수 있어요.", 0)
		return
	}
	var ins []store.FavInput
	for i, it := range q.Items {
		in, err := s.favInput(it)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("items[%d]: %s", i, err.Error()), 0)
			return
		}
		ins = append(ins, in)
	}
	fs, v, added, existing, err := s.o.DB.MergeFavorites(r.Context(), s.uid(w, r), ins, s.o.Now())
	if err != nil {
		s.internal(w, "favorites: merge", err)
		return
	}
	if added > 0 {
		s.syncGrids(r.Context())
	}
	s.writeFavList(w, r, http.StatusOK, fs, v, map[string]any{"added": added, "existing": existing})
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func (s *Server) favoritesDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "없는 즐겨찾기예요.", 0)
		return
	}
	v, err := s.o.DB.DeleteFavorite(r.Context(), s.uid(w, r), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "없는 즐겨찾기예요.", 0)
		return
	}
	if err != nil {
		s.internal(w, "favorites: delete", err)
		return
	}
	s.syncGrids(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id, "version": v})
}

func (s *Server) favoritesRename(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "없는 즐겨찾기예요.", 0)
		return
	}
	var q struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &q) {
		return
	}
	name := strings.TrimSpace(q.Name)
	if name == "" || utf8.RuneCountInString(name) > 40 {
		writeErr(w, http.StatusBadRequest, "bad_request", "이름은 1~40자예요.", 0)
		return
	}
	f, v, err := s.o.DB.RenameFavorite(r.Context(), s.uid(w, r), id, name)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "없는 즐겨찾기예요.", 0)
	case errors.Is(err, store.ErrConflict):
		writeErr(w, http.StatusConflict, "conflict", "같은 격자에 같은 이름의 즐겨찾기가 있어요.", 0)
	case err != nil:
		s.internal(w, "favorites: rename", err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"item": toFavJSON(f), "version": v})
	}
}

func (s *Server) favoritesOrder(w http.ResponseWriter, r *http.Request) {
	var q struct {
		IDs     []int64 `json:"ids"`
		Version *int64  `json:"version"`
	}
	if !readJSON(w, r, &q) {
		return
	}
	if q.Version == nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "version이 필요해요.", 0)
		return
	}
	fs, v, err := s.o.DB.ReorderFavorites(r.Context(), s.uid(w, r), q.IDs, *q.Version)
	if errors.Is(err, store.ErrConflict) {
		items, ierr := s.favItems(r.Context(), fs)
		if ierr != nil {
			s.internal(w, "favorites: order", ierr)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]any{"code": "conflict", "message": "다른 기기에서 목록이 바뀌었어요."},
			"items": items, "version": v,
		})
		return
	}
	if err != nil {
		s.internal(w, "favorites: order", err)
		return
	}
	s.writeFavList(w, r, http.StatusOK, fs, v, nil)
}

// --- 알림 설정 (§2.6) ---

func (s *Server) alertSettingsBody(ctx context.Context, userID int64) (map[string]any, error) {
	fs, _, err := s.o.DB.Favorites(ctx, userID)
	if err != nil {
		return nil, err
	}
	n, err := s.o.DB.PushSubCount(ctx)
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, f := range fs {
		items = append(items, map[string]any{"id": f.ID, "name": f.Name, "soon": f.Soon, "eve": f.Eve})
	}
	ru := s.o.Rules
	return map[string]any{
		"favorites": items,
		"rules": map[string]any{
			"soon_lead_h": []int{1, 3}, "soon_pop_min": ru.SoonPopMin, "eve_at": fmt.Sprintf("%02d:00", ru.EveHour),
			"eve_pop_min": ru.EvePopMin, "cooldown_h": int(ru.Cooldown / time.Hour),
		},
		"push_devices": n,
		"push_enabled": s.o.Push != nil,
	}, nil
}

func (s *Server) alertSettingsGet(w http.ResponseWriter, r *http.Request) {
	b, err := s.alertSettingsBody(r.Context(), s.uid(w, r))
	if err != nil {
		s.internal(w, "alerts: settings", err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) alertSettingsPatch(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Favorites []struct {
			ID   int64 `json:"id"`
			Soon *bool `json:"soon"`
			Eve  *bool `json:"eve"`
		} `json:"favorites"`
	}
	if !readJSON(w, r, &q) {
		return
	}
	var ps []store.AlertPatch
	for _, f := range q.Favorites {
		ps = append(ps, store.AlertPatch{ID: f.ID, Soon: f.Soon, Eve: f.Eve})
	}
	switch err := s.o.DB.PatchAlerts(r.Context(), s.uid(w, r), ps); {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "없는 즐겨찾기가 있어요.", 0)
		return
	case err != nil:
		s.internal(w, "alerts: patch", err)
		return
	}
	s.alertSettingsGet(w, r)
}

// --- 푸시 (§2.7) ---

func (s *Server) vapidKey(w http.ResponseWriter, r *http.Request) {
	if s.o.Push == nil {
		notConfigured(w, "푸시")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{"key": s.o.Push.PublicKey()})
}

func (s *Server) pushSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.o.Push == nil {
		notConfigured(w, "푸시")
		return
	}
	var q struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
		Label string `json:"label"`
	}
	if !readJSON(w, r, &q) {
		return
	}
	label := strings.TrimSpace(q.Label)
	if err := push.ValidateSubscription(q.Endpoint, q.Keys.P256dh, q.Keys.Auth); err != nil || utf8.RuneCountInString(label) > 40 {
		msg := "기기 이름은 40자 이하예요."
		if err != nil {
			msg = err.Error()
		}
		writeErr(w, http.StatusBadRequest, "bad_request", msg, 0)
		return
	}
	sub, err := s.o.DB.UpsertPushSub(r.Context(), store.PushSub{Endpoint: q.Endpoint, P256dh: q.Keys.P256dh, Auth: q.Keys.Auth, Label: label, UserID: s.uid(w, r)}, s.o.Now())
	if err != nil {
		s.internal(w, "push: subscribe", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": sub.ID, "label": sub.Label, "created_at": ts(sub.CreatedAt)})
}

func (s *Server) pushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Endpoint string `json:"endpoint"`
	}
	if !readJSON(w, r, &q) {
		return
	}
	if q.Endpoint == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "endpoint가 필요해요.", 0)
		return
	}
	if err := s.o.DB.DeleteUserPushSub(r.Context(), s.uid(w, r), q.Endpoint); err != nil {
		s.internal(w, "push: unsubscribe", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) pushTest(w http.ResponseWriter, r *http.Request) {
	if s.o.Push == nil {
		notConfigured(w, "푸시")
		return
	}
	var q struct {
		Endpoint string `json:"endpoint"`
	}
	if r.ContentLength != 0 && !readJSON(w, r, &q) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	rep, err := s.o.Push.SendUser(ctx, push.Message{Title: "my-weather", Body: "테스트 알림이에요.", URL: "/", Tag: "test",
		TTL: 5 * time.Minute, Urgency: "normal"}, s.uid(w, r), q.Endpoint)
	if err != nil {
		s.internal(w, "push: test", err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// --- 설정 화면 정보 ---

func (s *Server) settingsInfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := s.o.Now()
	var ops []string
	for _, op := range kma.Ops {
		ops = append(ops, string(op))
	}
	lc, err := s.o.DB.LastCollectedByOp(ctx, ops, kst.Day(now.AddDate(0, 0, -2)))
	if err != nil {
		s.internal(w, "settings: last collected", err)
		return
	}
	last := map[string]any{}
	for _, op := range ops {
		if v, ok := lc[op]; ok {
			last[op] = map[string]any{"base": kst.FromInt(v.Base), "at": ts(v.CalledAt)}
		} else {
			last[op] = nil
		}
	}
	out := map[string]any{"data_source": "기상청 단기예보 조회서비스 (공공데이터포털)", "last_collected": last, "admin": nil}
	if sess, ok := s.session(w, r); ok {
		pks, err := s.passkeyList(ctx, sess.UserID, sess.CredID)
		if err != nil {
			s.internal(w, "settings: passkeys", err)
			return
		}
		n, err := s.o.DB.PushSubCount(ctx)
		if err != nil {
			s.internal(w, "settings: push", err)
			return
		}
		account := map[string]any{"role": sess.Role, "name": sess.Name, "passkeys": pks, "push_devices": n}
		out["account"] = account
		if sess.Role == store.RoleAdmin {
			adm := map[string]any{"passkeys": pks, "push_devices": n, "role": sess.Role}
			if s.o.Collector != nil {
				adm["collect_grids"] = len(s.o.Collector.Grids())
			}
			if s.o.Budget != nil {
				snap := s.o.Budget.Snapshot()
				total := 0
				for _, c := range snap.ByOp {
					total += c
				}
				d := strconv.FormatInt(snap.Day, 10)
				if len(d) == 8 {
					d = d[:4] + "-" + d[4:6] + "-" + d[6:]
				}
				adm["calls_today"] = map[string]any{"date": d, "total": total, "by_op": snap.ByOp}
			}
			out["admin"] = adm
		}
		w.Header().Set("Cache-Control", "private, no-store")
	} else {
		w.Header().Set("Cache-Control", "private, max-age=60")
	}
	writeJSON(w, http.StatusOK, out)
}

// isAdmin은 /api/health 상세 표시용이다.
func (s *Server) isAdmin(w http.ResponseWriter, r *http.Request) bool {
	if info(r).key == ratelimit.KeyLocal {
		return true
	}
	sess, ok := s.session(w, r)
	return ok && sess.Role == store.RoleAdmin
}
