package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"myweather/internal/store"
)

type fakeClient struct {
	mu     sync.Mutex
	status map[string]int // host → 상태 코드
	reqs   []*http.Request
}

func (f *fakeClient) Do(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, r)
	st := f.status[r.URL.Host]
	if st == 0 {
		st = 201
	}
	return &http.Response{StatusCode: st, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}

func newSub(t *testing.T, host string) store.PushSub {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	rand.Read(auth)
	return store.PushSub{
		Endpoint: "https://" + host + "/push/abc", Label: host,
		P256dh: base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()),
		Auth:   base64.RawURLEncoding.EncodeToString(auth),
	}
}

func setup(t *testing.T) (*Sender, *store.DB, *fakeClient) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "p.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	pub, priv, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	fc := &fakeClient{status: map[string]int{}}
	s, err := New(Options{DB: db, PublicKey: pub, PrivateKey: priv, Subject: "mailto:khwoo8026@gmail.com", HTTPClient: fc})
	if err != nil {
		t.Fatal(err)
	}
	return s, db, fc
}

func TestKeysValidate(t *testing.T) {
	pub, priv, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateKeys(pub, priv); err != nil {
		t.Fatal(err)
	}
	pub2, _, _ := GenerateKeys()
	if err := ValidateKeys(pub2, priv); err == nil {
		t.Fatal("mismatched pair accepted")
	}
	if err := ValidateKeys("abc", priv); err == nil {
		t.Fatal("bad pub accepted")
	}
}

func TestSendAndCleanup(t *testing.T) {
	ctx := context.Background()
	s, db, fc := setup(t)
	now := time.Now()
	for _, h := range []string{"ok.push.example", "gone.push.example", "flaky.push.example"} {
		if _, err := db.UpsertPushSub(ctx, newSub(t, h), now); err != nil {
			t.Fatal(err)
		}
	}
	fc.status["gone.push.example"] = 410
	fc.status["flaky.push.example"] = 500

	m := Message{Title: "서교동", Body: "1~3시간 안에 비 소식 (15시 전후)", URL: "/?fav=1", Tag: "soon-1", TTL: time.Hour, Urgency: "high"}
	rep, err := s.SendAll(ctx, m, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep != (Report{Sent: 1, Failed: 2, Removed: 1}) {
		t.Fatalf("report %+v", rep)
	}
	// VAPID 헤더: sub가 mailto:mailto:가 아니어야 함
	r := fc.reqs[0]
	if r.Header.Get("TTL") != "3600" || r.Header.Get("Urgency") != "high" || r.Header.Get("Content-Encoding") != "aes128gcm" {
		t.Fatalf("headers %v", r.Header)
	}
	authz := r.Header.Get("Authorization")
	tok := strings.TrimPrefix(strings.Split(authz, ",")[0], "vapid t=")
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("authorization %q", authz)
	}
	claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c map[string]any
	json.Unmarshal(claims, &c)
	if c["sub"] != "mailto:khwoo8026@gmail.com" || c["aud"] != "https://ok.push.example" {
		t.Fatalf("claims %v", c)
	}
	subs, _ := db.PushSubs(ctx, "")
	if len(subs) != 2 {
		t.Fatalf("gone sub not removed: %d", len(subs))
	}
	// 실패 5회 연속이면 지움
	for i := 0; i < MaxFails-1; i++ {
		s.SendAll(ctx, m, "https://flaky.push.example/push/abc")
	}
	subs, _ = db.PushSubs(ctx, "")
	if len(subs) != 1 || !strings.Contains(subs[0].Endpoint, "ok.push") || subs[0].LastOKAt == 0 {
		t.Fatalf("subs %+v", subs)
	}
}

func TestValidateSubscription(t *testing.T) {
	s := newSub(t, "web.push.apple.com")
	if err := ValidateSubscription(s.Endpoint, s.P256dh, s.Auth); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSubscription("http://x/y", s.P256dh, s.Auth); err == nil {
		t.Fatal("http accepted")
	}
	if err := ValidateSubscription(s.Endpoint, s.P256dh, "AAAA"); err == nil {
		t.Fatal("short auth accepted")
	}
}
