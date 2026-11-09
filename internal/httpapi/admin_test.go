package httpapi

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"myweather/internal/auth"
	"myweather/internal/push"
	"myweather/internal/ratelimit"
	"myweather/internal/store"
)

const origin = "https://weather.example.com"

type adminEnv struct {
	*env
	au     *auth.Auth
	cookie string
	pushes *pushClient
}

type pushClient struct {
	mu sync.Mutex
	n  int
}

func (p *pushClient) Do(r *http.Request) (*http.Response, error) {
	p.mu.Lock()
	p.n++
	p.mu.Unlock()
	return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}

// newAdminEnv는 인증·푸시가 설정된 서버와 관리자 세션 쿠키를 만든다(세션은 DB에 직접 넣음. WebAuthn 의식은 auth 테스트에서 확인).
func newAdminEnv(t *testing.T, withPush bool) *adminEnv {
	t.Helper()
	e := newEnv(t)
	log := slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	clock := func() time.Time { return e.now }
	au, err := auth.New(auth.Options{DB: e.db, RPID: "weather.example.com", Origin: origin, Now: clock, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	ae := &adminEnv{env: e, au: au, pushes: &pushClient{}}
	opt := Options{
		DB: e.db, Collector: e.col, Budget: e.bud, Regions: regions, Version: "test", Now: clock, Log: log,
		IP: &ratelimit.ClientIP{TrustHeader: "X-Forwarded-For", Log: log}, Auth: au,
	}
	if withPush {
		pub, priv, _ := push.GenerateKeys()
		s, err := push.New(push.Options{DB: e.db, PublicKey: pub, PrivateKey: priv, Subject: "mailto:a@example.com", HTTPClient: ae.pushes, Now: clock, Log: log})
		if err != nil {
			t.Fatal(err)
		}
		opt.Push = s
	}
	e.h = New(opt).Handler()

	// 패스키 하나 + 세션
	ae.cookie = "test-session-cookie-value"
	h := sha256.Sum256([]byte(ae.cookie))
	ctx := context.Background()
	e.db.W.Exec(`INSERT INTO passkey (cred_id, public_key, sign_count, label, created_at) VALUES (x'0102', x'00', 0, 'iPhone', ?)`, e.now.Unix())
	if err := e.db.CreateSession(ctx, store.NewSession{TokenHash: h[:], CredID: []byte{1, 2}, ExpiresAt: e.now.Add(24 * time.Hour)}, e.now); err != nil {
		t.Fatal(err)
	}
	return ae
}

// do는 바깥 요청(프록시 경유)처럼 보낸다. admin이면 세션 쿠키를 붙이고, 상태 변경이면 Origin을 붙인다.
func (a *adminEnv) do(method, path, body string, admin bool, hdr ...string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, rd)
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	if method != "GET" {
		r.Header.Set("Origin", origin)
	}
	if admin {
		r.AddCookie(&http.Cookie{Name: a.au.CookieName(), Value: a.cookie})
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i+1] == "" {
			r.Header.Del(hdr[i])
		} else {
			r.Header.Set(hdr[i], hdr[i+1])
		}
	}
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, r)
	return w
}

func TestOriginAndAuthGuards(t *testing.T) {
	a := newAdminEnv(t, false)
	body := `{"name":"대관령면","region":"5176038000"}`
	if w := a.do("POST", "/api/favorites", body, true, "Origin", ""); w.Code != 403 {
		t.Fatalf("no origin: %d", w.Code)
	}
	if w := a.do("POST", "/api/favorites", body, true, "Origin", "https://evil.example"); w.Code != 403 {
		t.Fatalf("bad origin: %d", w.Code)
	}
	if w := a.do("POST", "/api/favorites", body, false); w.Code != 401 {
		t.Fatalf("no session: %d", w.Code)
	}
	if w := a.do("GET", "/api/favorites", "", false); w.Code != 401 {
		t.Fatalf("get no session: %d", w.Code)
	}
	if w := a.do("GET", "/api/timeline?fav=1", "", false); w.Code != 401 {
		t.Fatalf("fav timeline no session: %d", w.Code)
	}
	m := decode(t, a.do("GET", "/api/auth/me", "", false))
	if m["admin"] != false {
		t.Fatalf("me anon %v", m)
	}
	m = decode(t, a.do("GET", "/api/auth/me", "", true))
	if m["admin"] != true || m["passkeys"].(float64) != 1 {
		t.Fatalf("me admin %v", m)
	}
	// 관리자는 /api/health 상세
	if m := decode(t, a.do("GET", "/api/health", "", true)); m["collector"] == nil {
		t.Fatalf("admin health %v", m)
	}
	// 로그아웃 → 세션 삭제
	if w := a.do("POST", "/api/auth/logout", "", true); w.Code != 204 || !strings.Contains(w.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout %d %v", w.Code, w.Header())
	}
	if m := decode(t, a.do("GET", "/api/auth/me", "", true)); m["admin"] != false {
		t.Fatal("session survived logout")
	}
}

func TestAuthDisabled(t *testing.T) {
	e := newEnv(t) // Auth 없음
	r := httptest.NewRequest("POST", "/api/favorites", strings.NewReader(`{}`))
	r.Header.Set("Origin", origin)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("post without auth config: %d", w.Code)
	}
	if w := e.get("/api/favorites", "203.0.113.9"); w.Code != 503 {
		t.Fatalf("get favorites without auth config: %d", w.Code)
	}
	if m := decode(t, e.get("/api/auth/me", "203.0.113.9")); m["admin"] != false {
		t.Fatalf("me %v", m)
	}
	if w := e.get("/api/push/vapid-public-key", "203.0.113.9"); w.Code != 503 {
		t.Fatalf("vapid without config: %d", w.Code)
	}
}

func TestRegisterOptionsToken(t *testing.T) {
	a := newAdminEnv(t, false)
	if w := a.do("POST", "/api/auth/register/options", `{"token":"MW-AAAA-BBBB-CCCC","label":"iPhone"}`, false); w.Code != 403 {
		t.Fatalf("bad token: %d %s", w.Code, w.Body)
	}
	tok, _, err := auth.IssueEnrollToken(context.Background(), a.db, 10*time.Minute, false, a.now)
	if err != nil {
		t.Fatal(err)
	}
	w := a.do("POST", "/api/auth/register/options", `{"token":"`+strings.ToLower(tok)+`","label":"iPhone"}`, false)
	if w.Code != 200 {
		t.Fatalf("options %d %s", w.Code, w.Body)
	}
	var o struct {
		PublicKey struct {
			RP struct {
				ID string `json:"id"`
			} `json:"rp"`
			AuthenticatorSelection map[string]any `json:"authenticatorSelection"`
			ExcludeCredentials     []any          `json:"excludeCredentials"`
		} `json:"publicKey"`
	}
	json.Unmarshal(w.Body.Bytes(), &o)
	if o.PublicKey.RP.ID != "weather.example.com" || o.PublicKey.AuthenticatorSelection["residentKey"] != "required" ||
		o.PublicKey.AuthenticatorSelection["userVerification"] != "required" || len(o.PublicKey.ExcludeCredentials) != 1 {
		t.Fatalf("options %s", w.Body)
	}
	if w := a.do("POST", "/api/auth/login/options", "", false); w.Code != 200 || !strings.Contains(w.Body.String(), `"challenge"`) {
		t.Fatalf("login options %d %s", w.Code, w.Body)
	}
	if w := a.do("POST", "/api/auth/login/verify", `{"credential":{"id":"x"}}`, false); w.Code != 401 {
		t.Fatalf("login verify garbage: %d %s", w.Code, w.Body)
	}
}

func favIDs(t *testing.T, m map[string]any) []float64 {
	var out []float64
	for _, it := range m["items"].([]any) {
		out = append(out, it.(map[string]any)["id"].(float64))
	}
	return out
}

func TestFavoritesFlow(t *testing.T) {
	a := newAdminEnv(t, false)
	if len(a.col.Grids()) != 1 {
		t.Fatal("setup grids")
	}
	w := a.do("POST", "/api/favorites", `{"name":"대관령면","region":"5176038000","nx":89,"ny":130}`, true)
	if w.Code != 201 {
		t.Fatalf("add %d %s", w.Code, w.Body)
	}
	m := decode(t, w)
	item := m["item"].(map[string]any)
	if item["pos"].(float64) != 0 || item["alerts"].(map[string]any)["soon"] != false || m["version"].(float64) != 1 {
		t.Fatalf("add %v", m)
	}
	if w := a.do("POST", "/api/favorites", `{"name":"대관령면","region":"5176038000","nx":89,"ny":130}`, true); w.Code != 200 {
		t.Fatalf("idempotent add %d", w.Code)
	}
	if g := a.col.Grids(); len(g) != 2 || g[1] != [2]int{89, 130} {
		t.Fatalf("collector grids %v", g)
	}
	if w := a.do("POST", "/api/favorites", `{"region":"5176038000","nx":59,"ny":126}`, true); w.Code != 400 {
		t.Fatalf("region/grid mismatch %d", w.Code)
	}
	// region만 주면 이름·격자는 표에서
	w = a.do("POST", "/api/favorites", `{"region":"1144066000"}`, true)
	if m := decode(t, w); w.Code != 201 || m["item"].(map[string]any)["name"] != "서교동" || m["item"].(map[string]any)["nx"].(float64) != 59 {
		t.Fatalf("region only %d %v", w.Code, m)
	}
	// 합치기: 하나는 기존, 하나는 새것(region 없음)
	w = a.do("POST", "/api/favorites/merge", `{"items":[{"name":"서교동","region":"1144066000","nx":59,"ny":126},{"name":"바다","nx":52,"ny":33}]}`, true)
	m = decode(t, w)
	if w.Code != 200 || m["added"].(float64) != 1 || m["existing"].(float64) != 1 || len(m["items"].([]any)) != 3 {
		t.Fatalf("merge %d %v", w.Code, m)
	}
	// 같은 요청 다시: added 0
	m = decode(t, a.do("POST", "/api/favorites/merge", `{"items":[{"name":"바다","nx":52,"ny":33}]}`, true))
	if m["added"].(float64) != 0 {
		t.Fatalf("merge idempotent %v", m)
	}
	m = decode(t, a.do("GET", "/api/favorites", "", true))
	ids := favIDs(t, m)
	ver := m["version"].(float64)
	if len(ids) != 3 || ver != 3 {
		t.Fatalf("list %v", m)
	}
	// 수집 격자(59,126)는 now가 채워질 수 있고, 데이터 없는 격자는 null
	if it := m["items"].([]any)[2].(map[string]any); it["now"] != nil {
		t.Fatalf("now for empty grid %v", it)
	}
	// 순서: 틀린 version → 409 + 목록
	w = a.do("PUT", "/api/favorites/order", `{"ids":[3,1,2],"version":1}`, true)
	if w.Code != 409 || decode(t, w)["version"].(float64) != 3 {
		t.Fatalf("order conflict %d %s", w.Code, w.Body)
	}
	w = a.do("PUT", "/api/favorites/order", `{"ids":[3,1,2],"version":3}`, true)
	if m := decode(t, w); w.Code != 200 || favIDs(t, m)[0] != 3 || m["version"].(float64) != 4 {
		t.Fatalf("order %d %s", w.Code, w.Body)
	}
	// 이름 바꾸기
	w = a.do("PATCH", "/api/favorites/2", `{"name":"회사"}`, true)
	if m := decode(t, w); w.Code != 200 || m["item"].(map[string]any)["name"] != "회사" {
		t.Fatalf("rename %d %s", w.Code, w.Body)
	}
	// 알림 설정
	w = a.do("PATCH", "/api/alerts/settings", `{"favorites":[{"id":2,"soon":true,"eve":true}]}`, true)
	m = decode(t, w)
	if w.Code != 200 || m["rules"].(map[string]any)["soon_pop_min"].(float64) != 30 {
		t.Fatalf("alerts %d %s", w.Code, w.Body)
	}
	found := false
	for _, f := range m["favorites"].([]any) {
		f := f.(map[string]any)
		if f["id"].(float64) == 2 && f["soon"] == true && f["eve"] == true {
			found = true
		}
	}
	if !found {
		t.Fatalf("alert not set %v", m)
	}
	if w := a.do("PATCH", "/api/alerts/settings", `{"favorites":[{"id":99,"soon":true}]}`, true); w.Code != 404 {
		t.Fatalf("alert unknown id %d", w.Code)
	}
	// 즐겨찾기 타임라인 (이름은 즐겨찾기 이름)
	w = a.do("GET", "/api/timeline?fav=2", "", true)
	if w.Code != 200 && w.Code != 503 {
		t.Fatalf("fav timeline %d %s", w.Code, w.Body)
	}
	if w.Code == 200 && decode(t, w)["place"].(map[string]any)["name"] != "회사" {
		t.Fatalf("fav timeline name %s", w.Body)
	}
	// 삭제: 89,130 격자가 수집 대상에서 빠짐
	if w := a.do("DELETE", "/api/favorites/1", "", true); w.Code != 200 || decode(t, w)["deleted"].(float64) != 1 {
		t.Fatalf("delete %d", w.Code)
	}
	if w := a.do("DELETE", "/api/favorites/1", "", true); w.Code != 404 {
		t.Fatalf("delete again %d", w.Code)
	}
	for _, g := range a.col.Grids() {
		if g == [2]int{89, 130} {
			t.Fatalf("grid not removed %v", a.col.Grids())
		}
	}
	m = decode(t, a.do("GET", "/api/favorites", "", true))
	for i, it := range m["items"].([]any) {
		if it.(map[string]any)["pos"].(float64) != float64(i) {
			t.Fatalf("pos not compacted %v", m)
		}
	}
}

func TestPushEndpoints(t *testing.T) {
	a := newAdminEnv(t, true)
	m := decode(t, a.do("GET", "/api/push/vapid-public-key", "", false))
	if k, _ := m["key"].(string); len(k) < 80 {
		t.Fatalf("vapid key %v", m)
	}
	kp, _ := ecdh.P256().GenerateKey(rand.Reader)
	authb := make([]byte, 16)
	rand.Read(authb)
	sub := map[string]any{"endpoint": "https://web.push.apple.com/QGabc", "expirationTime": nil,
		"keys":  map[string]any{"p256dh": base64.RawURLEncoding.EncodeToString(kp.PublicKey().Bytes()), "auth": base64.RawURLEncoding.EncodeToString(authb)},
		"label": "iPhone"}
	b, _ := json.Marshal(sub)
	w := a.do("POST", "/api/push/subscriptions", string(b), true)
	if w.Code != 201 || decode(t, w)["label"] != "iPhone" {
		t.Fatalf("subscribe %d %s", w.Code, w.Body)
	}
	if w := a.do("POST", "/api/push/subscriptions", `{"endpoint":"http://x","keys":{"p256dh":"a","auth":"b"}}`, true); w.Code != 400 {
		t.Fatalf("bad sub %d", w.Code)
	}
	w = a.do("POST", "/api/push/test", "", true)
	if m := decode(t, w); w.Code != 200 || m["sent"].(float64) != 1 || a.pushes.n != 1 {
		t.Fatalf("test push %d %s", w.Code, w.Body)
	}
	if m := decode(t, a.do("GET", "/api/alerts/settings", "", true)); m["push_devices"].(float64) != 1 {
		t.Fatalf("push_devices %v", m)
	}
	if w := a.do("DELETE", "/api/push/subscriptions", `{"endpoint":"https://web.push.apple.com/QGabc"}`, true); w.Code != 204 {
		t.Fatalf("unsubscribe %d", w.Code)
	}
	if w := a.do("DELETE", "/api/push/subscriptions", `{"endpoint":"https://web.push.apple.com/QGabc"}`, true); w.Code != 204 {
		t.Fatalf("unsubscribe idempotent %d", w.Code)
	}
}

func TestSettingsInfoAndNow(t *testing.T) {
	a := newAdminEnv(t, false)
	// 수집 격자 데이터 받기 (가짜 KMA)
	a.col.Step(context.Background())
	m := decode(t, a.do("GET", "/api/settings/info", "", false))
	if m["admin"] != nil || m["last_collected"].(map[string]any)["getUltraSrtNcst"] == nil {
		t.Fatalf("public info %v", m)
	}
	m = decode(t, a.do("GET", "/api/settings/info", "", true))
	adm := m["admin"].(map[string]any)
	pk := adm["passkeys"].([]any)[0].(map[string]any)
	if pk["label"] != "iPhone" || pk["current"] != true || pk["id"] != "AQI" || adm["collect_grids"].(float64) != 1 {
		t.Fatalf("admin info %v", adm)
	}
	if ct := adm["calls_today"].(map[string]any); ct["total"].(float64) < 3 || ct["date"] != "2026-11-10" {
		t.Fatalf("calls_today %v", ct)
	}
	// /api/now: 수집 격자는 값, 모르는 격자는 pending (KMA 호출 없음)
	before := len(a.kma.Requests())
	w := a.do("GET", "/api/now?places=59,126;89,130", "", false)
	m = decode(t, w)
	items, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("now %d %s", w.Code, w.Body)
	}
	n0, n1 := items[0].(map[string]any), items[1].(map[string]any)
	if n0["tmp"] == nil || n0["src"] != "ncst" || n0["observed_at"] == nil || n1["pending"] != true {
		t.Fatalf("now %v", m)
	}
	if len(a.kma.Requests()) != before {
		t.Fatal("/api/now called KMA")
	}
	if w := a.do("GET", "/api/now?places=1,1;"+strings.Repeat("2,2;", 20), "", false); w.Code != 400 {
		t.Fatalf("too many places %d", w.Code)
	}
	// 패스키 지우기: 하나뿐이면 409
	if w := a.do("DELETE", "/api/auth/passkeys/AQI", "", true); w.Code != 409 {
		t.Fatalf("delete last passkey %d", w.Code)
	}
}
