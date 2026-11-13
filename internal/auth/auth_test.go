package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"

	"myweather/internal/kst"
	"myweather/internal/store"
)

const (
	testRPID   = "weather.example.com"
	testOrigin = "https://weather.example.com"
)

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// softKey는 테스트용 소프트웨어 인증기(P-256, none attestation)다. 플래그는 iCloud 패스키처럼 UP·UV·BE·BS.
type softKey struct {
	priv   *ecdsa.PrivateKey
	credID []byte
	count  uint32
	rpID   string
	origin string
	handle []byte // 등록 때 받은 user.id
}

func newSoftKey(t *testing.T) *softKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	rand.Read(id)
	return &softKey{priv: k, credID: id, rpID: testRPID, origin: testOrigin}
}

const flags = 0x01 | 0x04 | 0x08 | 0x10 // UP UV BE BS

func (k *softKey) authData(at bool) []byte {
	rp := sha256.Sum256([]byte(k.rpID))
	b := append([]byte{}, rp[:]...)
	f := byte(flags)
	if at {
		f |= 0x40
	}
	b = append(b, f)
	b = binary.BigEndian.AppendUint32(b, k.count)
	if at {
		b = append(b, make([]byte, 16)...) // AAGUID
		b = binary.BigEndian.AppendUint16(b, uint16(len(k.credID)))
		b = append(b, k.credID...)
		x := k.priv.PublicKey.X.FillBytes(make([]byte, 32))
		y := k.priv.PublicKey.Y.FillBytes(make([]byte, 32))
		cose, err := webauthncbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
		if err != nil {
			panic(err)
		}
		b = append(b, cose...)
	}
	return b
}

func clientData(typ, challenge, origin string) []byte {
	b, _ := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": origin, "crossOrigin": false})
	return b
}

// create는 navigator.credentials.create() 응답(JSON)을 만든다.
func (k *softKey) create(t *testing.T, opts []byte) []byte {
	t.Helper()
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			User      struct {
				ID string `json:"id"`
			} `json:"user"`
			RP struct {
				ID string `json:"id"`
			} `json:"rp"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(opts, &o); err != nil {
		t.Fatal(err)
	}
	if o.PublicKey.RP.ID != k.rpID {
		t.Fatalf("rp.id=%q", o.PublicKey.RP.ID)
	}
	h, err := base64.RawURLEncoding.DecodeString(o.PublicKey.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	k.handle = h
	att, err := webauthncbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": k.authData(true)})
	if err != nil {
		t.Fatal(err)
	}
	cd := clientData("webauthn.create", o.PublicKey.Challenge, k.origin)
	b, _ := json.Marshal(map[string]any{
		"id": b64(k.credID), "rawId": b64(k.credID), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64(cd), "attestationObject": b64(att), "transports": []string{"internal", "hybrid"}},
	})
	return b
}

// get은 navigator.credentials.get() 응답(JSON)을 만든다.
func (k *softKey) get(t *testing.T, opts []byte) []byte {
	t.Helper()
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(opts, &o); err != nil {
		t.Fatal(err)
	}
	ad := k.authData(false)
	cd := clientData("webauthn.get", o.PublicKey.Challenge, k.origin)
	cdh := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, ad...), cdh[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, k.priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{
		"id": b64(k.credID), "rawId": b64(k.credID), "type": "public-key",
		"response": map[string]any{"clientDataJSON": b64(cd), "authenticatorData": b64(ad), "signature": b64(sig), "userHandle": b64(k.handle)},
	})
	return b
}

func newTest(t *testing.T) (*Auth, *store.DB, *time.Time) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "a.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(kst.Loc)
	a, err := New(Options{DB: db, RPID: testRPID, Origin: testOrigin, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	return a, db, &now
}

func mustJSON(t *testing.T, v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func register(t *testing.T, a *Auth, k *softKey, tok, label string) (Result, error) {
	t.Helper()
	ctx := context.Background()
	cc, err := a.BeginRegistration(ctx, tok, label)
	if err != nil {
		return Result{}, err
	}
	return a.FinishRegistration(ctx, tok, k.create(t, mustJSON(t, cc)), "test")
}

func TestTokenFormat(t *testing.T) {
	tok, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != len("MW-XXXX-XXXX-XXXX") || !strings.HasPrefix(tok, "MW-") {
		t.Fatalf("token %q", tok)
	}
	for _, v := range []string{strings.ToLower(tok), strings.ReplaceAll(tok, "-", " "), strings.TrimPrefix(tok, "MW-")} {
		if string(HashToken(v)) != string(HashToken(tok)) {
			t.Fatalf("normalize %q", v)
		}
	}
}

func TestRegisterLoginFlow(t *testing.T) {
	ctx := context.Background()
	a, db, now := newTest(t)
	tok, exp, err := IssueEnrollToken(ctx, db, DefaultTokenTTL, false, store.RoleAdmin, *now)
	if err != nil || !exp.Equal(now.Add(10*time.Minute)) {
		t.Fatal(tok, exp, err)
	}
	if _, err := a.BeginRegistration(ctx, "MW-AAAA-BBBB-CCCC", "x"); !errors.Is(err, ErrBadToken) {
		t.Fatalf("wrong token: %v", err)
	}
	k := newSoftKey(t)
	res, err := register(t, a, k, strings.ToLower(tok), "iPhone 15")
	if err != nil {
		t.Fatal(err)
	}
	if res.Cookie == "" || res.Passkey.Label != "iPhone 15" {
		t.Fatalf("res %+v", res)
	}
	ps, _ := db.Passkeys(ctx, 0)
	if len(ps) != 1 || !ps[0].BackupEligible || ps[0].Transports != "internal,hybrid" {
		t.Fatalf("passkeys %+v", ps)
	}
	// 토큰은 한 번만
	if _, err := register(t, a, newSoftKey(t), tok, "again"); !errors.Is(err, ErrBadToken) {
		t.Fatalf("reuse: %v", err)
	}

	// 세션 쿠키로 요청 인증
	r := httptest.NewRequest("GET", "/api/auth/me", nil)
	r.AddCookie(&http.Cookie{Name: a.CookieName(), Value: res.Cookie})
	if _, ok := a.Session(nil, r); !ok {
		t.Fatal("session not found")
	}

	// 로그인 (서명 카운터 0 그대로: iCloud처럼)
	ca, err := a.BeginLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cj := mustJSON(t, ca)
	if !strings.Contains(string(cj), `"userVerification":"required"`) || strings.Contains(string(cj), "allowCredentials\":[{") {
		t.Fatalf("login options %s", cj)
	}
	resp := k.get(t, cj)
	lr, err := a.FinishLogin(ctx, resp, "ua")
	if err != nil {
		t.Fatal(err)
	}
	if lr.Cookie == "" || lr.Cookie == res.Cookie {
		t.Fatal("login cookie")
	}
	// 같은 응답 재사용(challenge 한 번만)
	if _, err := a.FinishLogin(ctx, resp, "ua"); !errors.Is(err, ErrChallenge) {
		t.Fatalf("replay: %v", err)
	}
	ps, _ = db.Passkeys(ctx, 0)
	if ps[0].LastUsedAt == 0 {
		t.Fatal("last_used_at not set")
	}

	// 다른 키(등록 안 됨)로 로그인 실패
	ca, _ = a.BeginLogin(ctx)
	k2 := newSoftKey(t)
	k2.handle = k.handle
	if _, err := a.FinishLogin(ctx, k2.get(t, mustJSON(t, ca)), "ua"); !errors.Is(err, ErrVerify) {
		t.Fatalf("unknown key: %v", err)
	}
	// 다른 Origin에서 만든 응답은 실패
	ca, _ = a.BeginLogin(ctx)
	k.origin = "https://evil.example"
	if _, err := a.FinishLogin(ctx, k.get(t, mustJSON(t, ca)), "ua"); !errors.Is(err, ErrVerify) {
		t.Fatalf("bad origin: %v", err)
	}
	k.origin = testOrigin

	// 마지막 패스키는 못 지움
	if err := db.DeletePasskey(ctx, ps[0].UserID, k.credID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("delete last: %v", err)
	}
}

func TestRevokeExisting(t *testing.T) {
	ctx := context.Background()
	a, db, now := newTest(t)
	tok, _, _ := IssueEnrollToken(ctx, db, time.Minute, false, store.RoleAdmin, *now)
	k1 := newSoftKey(t)
	r1, err := register(t, a, k1, tok, "old")
	if err != nil {
		t.Fatal(err)
	}
	tok2, _, _ := IssueEnrollToken(ctx, db, time.Minute, true, store.RoleAdmin, *now)
	k2 := newSoftKey(t)
	r2, err := register(t, a, k2, tok2, "new")
	if err != nil || !r2.Revoked {
		t.Fatalf("revoke: %+v %v", r2, err)
	}
	ps, _ := db.Passkeys(ctx, 0)
	if len(ps) != 1 || ps[0].Label != "new" {
		t.Fatalf("passkeys %+v", ps)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: a.CookieName(), Value: r1.Cookie})
	if _, ok := a.Session(nil, r); ok {
		t.Fatal("old session survived revoke")
	}
}

func TestTokenExpiryAndChallengeTTL(t *testing.T) {
	ctx := context.Background()
	a, db, now := newTest(t)
	tok, _, _ := IssueEnrollToken(ctx, db, 10*time.Minute, false, store.RoleMember, *now)
	cc, err := a.BeginRegistration(ctx, tok, "x")
	if err != nil {
		t.Fatal(err)
	}
	k := newSoftKey(t)
	body := k.create(t, mustJSON(t, cc))
	*now = now.Add(3 * time.Minute) // challenge 2분 초과
	if _, err := a.FinishRegistration(ctx, tok, body, ""); !errors.Is(err, ErrChallenge) {
		t.Fatalf("expired challenge: %v", err)
	}
	*now = now.Add(8 * time.Minute) // 토큰 10분 초과
	if _, err := a.BeginRegistration(ctx, tok, "x"); !errors.Is(err, ErrBadToken) {
		t.Fatalf("expired token: %v", err)
	}
	if _, _, err := IssueEnrollToken(ctx, db, 25*time.Hour, false, store.RoleAdmin, *now); err == nil {
		t.Fatal("ttl > 24h accepted")
	}
}

func TestSessionSlidingAndCookie(t *testing.T) {
	ctx := context.Background()
	a, db, now := newTest(t)
	tok, _, _ := IssueEnrollToken(ctx, db, time.Minute, false, store.RoleAdmin, *now)
	res, err := register(t, a, newSoftKey(t), tok, "x")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	a.SetCookie(w, res.Cookie, res.ExpiresAt)
	sc := w.Header().Get("Set-Cookie")
	for _, want := range []string{"__Host-mw_session=", "Path=/", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(sc, want) {
			t.Fatalf("cookie %q missing %q", sc, want)
		}
	}
	if strings.Contains(sc, "Domain=") {
		t.Fatal("__Host- cookie must not have Domain")
	}
	req := func() (*httptest.ResponseRecorder, bool) {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(&http.Cookie{Name: a.CookieName(), Value: res.Cookie})
		w := httptest.NewRecorder()
		_, ok := a.Session(w, r)
		return w, ok
	}
	if w, ok := req(); !ok || w.Header().Get("Set-Cookie") != "" {
		t.Fatal("fresh session should not refresh")
	}
	*now = now.Add(25 * time.Hour)
	if w, ok := req(); !ok || w.Header().Get("Set-Cookie") == "" {
		t.Fatal("day-old session should refresh")
	}
	*now = now.Add(31 * 24 * time.Hour)
	if _, ok := req(); ok {
		t.Fatal("expired session accepted")
	}
}

func TestCheckOrigin(t *testing.T) {
	a, _, _ := newTest(t)
	for o, want := range map[string]bool{testOrigin: true, "https://WEATHER.example.com": true, "": false, "https://evil.example": false, "null": false} {
		r := httptest.NewRequest("POST", "/api/x", nil)
		if o != "" {
			r.Header.Set("Origin", o)
		}
		if got := a.CheckOrigin(r); got != want {
			t.Errorf("origin %q: %v", o, got)
		}
	}
}
