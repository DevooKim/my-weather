// Package auth는 패스키(WebAuthn) 등록·로그인, 일회용 등록 토큰, 세션 쿠키, Origin 검사를 맡는다.
//
//   - 사용자는 여러 명이다. 역할은 admin(관리자)과 member(일반)다. 기존 단일 계정은 마이그레이션에서 관리자가 된다.
//   - challenge는 메모리에 2분 둔다. 검증 요청의 clientDataJSON에서 challenge를 꺼내 찾고, 찾으면 바로 지운다(한 번만 사용).
//   - 등록은 일회용 토큰이 있어야 한다. 토큰을 만들 때 사용자를 만들고, verify에서 그 사용자에게 패스키를 붙인다.
//   - 세션 쿠키 값은 무작위 32바이트, DB에는 SHA-256만 둔다. 30일 슬라이딩(하루 한 번 연장).
//   - 안드로이드 Credential Manager는 로그인 응답에 userHandle을 비우는 경우가 있다. 그때는 credential id로 사용자를 찾는다.
package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"myweather/internal/kst"
	"myweather/internal/store"
)

// 기본값
const (
	ChallengeTTL    = 2 * time.Minute
	SessionTTL      = 30 * 24 * time.Hour
	SessionRefresh  = 24 * time.Hour
	DefaultTokenTTL = 10 * time.Minute
	MaxTokenTTL     = 24 * time.Hour
	maxPending    = 256
	RPDisplayName = "my-weather"
)

// 오류
var (
	ErrBadToken  = errors.New("auth: enroll token invalid, used or expired")
	ErrChallenge = errors.New("auth: unknown or expired challenge")
	ErrVerify    = errors.New("auth: credential verification failed")
	ErrBusy      = errors.New("auth: too many pending ceremonies")
	ErrDuplicate = errors.New("auth: passkey already registered")
)

// Options는 인증 설정이다.
type Options struct {
	DB     *store.DB
	RPID   string
	Origin string // https://<RPID> (개발은 http://localhost:포트)
	Now    func() time.Time
	Log    *slog.Logger
}

type pending struct {
	sd        webauthn.SessionData
	register  bool
	tokenHash []byte
	label     string
	exp       time.Time
}

// Auth는 인증 서비스다.
type Auth struct {
	db     *store.DB
	wa     *webauthn.WebAuthn
	origin string
	secure bool
	now    func() time.Time
	log    *slog.Logger

	mu      sync.Mutex
	pending map[string]*pending // challenge(base64url) → 대기 중인 의식
}

// New는 인증 서비스를 만든다.
func New(o Options) (*Auth, error) {
	if o.Now == nil {
		o.Now = kst.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.RPID == "" || o.Origin == "" {
		return nil, errors.New("auth: RP ID and Origin required")
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:                  o.RPID,
		RPDisplayName:         RPDisplayName,
		RPOrigins:             []string{o.Origin},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			RequireResidentKey: protocol.ResidentKeyRequired(),
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			UserVerification:   protocol.VerificationRequired,
		},
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: ChallengeTTL, TimeoutUVD: ChallengeTTL},
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: ChallengeTTL, TimeoutUVD: ChallengeTTL},
		},
	})
	if err != nil {
		return nil, err
	}
	return &Auth{
		db: o.DB, wa: wa, origin: strings.ToLower(o.Origin), secure: strings.HasPrefix(o.Origin, "https://"),
		now: o.Now, log: o.Log, pending: map[string]*pending{},
	}, nil
}

// Origin은 설정된 화면 Origin이다.
func (a *Auth) Origin() string { return a.origin }

// --- 등록 토큰 ---

// NewToken은 MW-XXXX-XXXX-XXXX 형식의 토큰(30자 알파벳 12글자, 약 59비트)을 만든다.
func NewToken() (string, error) {
	const alpha = "23456789ABCDEFGHJKMNPQRSTVWXYZ" // 헷갈리는 0·O·1·I·L·U를 뺀 30자
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("MW")
	for i := 0; i < 12; i++ {
		if i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(alpha[int(b[i])%len(alpha)]) // 256 mod 30 치우침(일부 글자 9/256 vs 8/256)은 12글자 약 59비트에 비해 무시할 수준
	}
	return sb.String(), nil
}

// NormalizeToken은 대소문자·공백·하이픈을 무시하고 MW 접두어를 붙인 정규형을 돌려준다.
func NormalizeToken(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		}
	}
	t := sb.String()
	if len(t) == 12 {
		t = "MW" + t
	}
	return t
}

// HashToken은 정규형 토큰의 SHA-256이다.
func HashToken(s string) []byte {
	h := sha256.Sum256([]byte(NormalizeToken(s)))
	return h[:]
}

// IssueEnrollToken은 등록 토큰을 만들어 해시만 저장하고, 토큰 원문과 만료 시각을 돌려준다.
// role이 비어 있으면 관리자다. revoke면 기존 관리자(없으면 새로 만든 관리자)의 패스키를 등록 성공 때 지운다.
func IssueEnrollToken(ctx context.Context, db *store.DB, ttl time.Duration, revoke bool, role string, now time.Time) (string, time.Time, error) {
	return IssueEnrollTokenAs(ctx, db, ttl, revoke, role, "cli", now)
}

// IssueEnrollTokenAs는 source를 지정하는 IssueEnrollToken이다.
func IssueEnrollTokenAs(ctx context.Context, db *store.DB, ttl time.Duration, revoke bool, role, source string, now time.Time) (string, time.Time, error) {
	if ttl <= 0 || ttl > MaxTokenTTL {
		return "", time.Time{}, fmt.Errorf("enroll-token: ttl must be in (0, %s]", MaxTokenTTL)
	}
	if role == "" {
		role = store.RoleAdmin
	}
	if !store.ValidRole(role) {
		return "", time.Time{}, fmt.Errorf("enroll-token: role must be admin or member")
	}
	var user store.User
	var err error
	if revoke {
		user, err = db.OldestAdmin(ctx)
		if errors.Is(err, store.ErrNotFound) {
			user, err = db.CreateUser(ctx, store.RoleAdmin, now)
		}
		role = store.RoleAdmin
	} else {
		user, err = db.CreateUser(ctx, role, now)
	}
	if err != nil {
		return "", time.Time{}, err
	}
	tok, err := NewToken()
	if err != nil {
		return "", time.Time{}, err
	}
	exp := now.Add(ttl)
	if source == "" {
		source = "cli"
	}
	if err := db.InsertEnrollToken(ctx, HashToken(tok), exp, revoke, role, user.ID, source, now); err != nil {
		return "", time.Time{}, err
	}
	return tok, exp, nil
}

// --- 사용자 ---

type waUser struct {
	id      []byte
	name    string
	display string
	creds   []webauthn.Credential
}

func (u *waUser) WebAuthnID() []byte                         { return u.id }
func (u *waUser) WebAuthnName() string                       { return u.name }
func (u *waUser) WebAuthnDisplayName() string                { return u.display }
func (u *waUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func toCredential(p store.Passkey) webauthn.Credential {
	var tr []protocol.AuthenticatorTransport
	for _, t := range strings.Split(p.Transports, ",") {
		if t != "" {
			tr = append(tr, protocol.AuthenticatorTransport(t))
		}
	}
	return webauthn.Credential{
		ID: p.CredID, PublicKey: p.PublicKey, AttestationType: p.AttestationType, Transport: tr,
		Flags:         webauthn.CredentialFlags{UserPresent: true, UserVerified: p.UserVerified, BackupEligible: p.BackupEligible, BackupState: p.BackupState},
		Authenticator: webauthn.Authenticator{AAGUID: p.AAGUID, SignCount: p.SignCount},
	}
}

func (a *Auth) loadUser(ctx context.Context, u store.User) (*waUser, error) {
	ps, err := a.db.Passkeys(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	out := &waUser{id: u.Handle, name: fmt.Sprintf("%s-%d", u.Role, u.ID), display: u.Name}
	for _, p := range ps {
		out.creds = append(out.creds, toCredential(p))
	}
	return out, nil
}

// --- 대기 중인 의식 ---

func (a *Auth) put(p *pending) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for k, v := range a.pending {
		if !now.Before(v.exp) {
			delete(a.pending, k)
		}
	}
	if len(a.pending) >= maxPending {
		return ErrBusy
	}
	a.pending[p.sd.Challenge] = p
	return nil
}

func (a *Auth) take(challenge string, register bool) (*pending, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.pending[challenge]
	if p == nil || p.register != register {
		return nil, ErrChallenge
	}
	delete(a.pending, challenge)
	if !a.now().Before(p.exp) {
		return nil, ErrChallenge
	}
	return p, nil
}

// --- 등록 ---

// BeginRegistration은 토큰을 확인하고(소멸시키지 않음) 등록 옵션을 만든다.
func (a *Auth) BeginRegistration(ctx context.Context, token, label string) (*protocol.CredentialCreation, error) {
	th := HashToken(token)
	tok, err := a.db.ValidEnrollToken(ctx, th, a.now())
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrBadToken
	}
	if err != nil {
		return nil, err
	}
	owner, err := a.db.UserByID(ctx, tok.UserID)
	if err != nil {
		return nil, err
	}
	u, err := a.loadUser(ctx, owner)
	if err != nil {
		return nil, err
	}
	var excl []protocol.CredentialDescriptor
	if !tok.RevokeExisting {
		excl = webauthn.Credentials(u.creds).CredentialDescriptors()
	}
	cc, sd, err := a.wa.BeginRegistration(u, webauthn.WithExclusions(excl))
	if err != nil {
		return nil, err
	}
	if err := a.put(&pending{sd: *sd, register: true, tokenHash: th, label: label, exp: a.now().Add(ChallengeTTL)}); err != nil {
		return nil, err
	}
	return cc, nil
}

// Result는 등록·로그인 성공 결과다. Cookie는 Set-Cookie에 넣을 세션 값이다.
type Result struct {
	Passkey   store.Passkey
	Cookie    string
	ExpiresAt time.Time
	Revoked   bool // 등록 때 기존 패스키·세션을 지웠음
}

func challengeOf(c protocol.CollectedClientData) string { return c.Challenge }

// FinishRegistration은 등록 응답을 검증하고 토큰 소멸·패스키 저장·세션 생성을 한 트랜잭션으로 한다.
func (a *Auth) FinishRegistration(ctx context.Context, token string, credJSON []byte, userAgent string) (Result, error) {
	parsed, err := protocol.ParseCredentialCreationResponseBytes(credJSON)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrVerify, err)
	}
	p, err := a.take(challengeOf(parsed.Response.CollectedClientData), true)
	if err != nil {
		return Result{}, err
	}
	th := HashToken(token)
	if !bytes.Equal(th, p.tokenHash) {
		return Result{}, ErrBadToken
	}
	tok, err := a.db.ValidEnrollToken(ctx, th, a.now())
	if errors.Is(err, store.ErrNotFound) {
		return Result{}, ErrBadToken
	}
	if err != nil {
		return Result{}, err
	}
	owner, err := a.db.UserByID(ctx, tok.UserID)
	if err != nil {
		return Result{}, err
	}
	u, err := a.loadUser(ctx, owner)
	if err != nil {
		return Result{}, err
	}
	cred, err := a.wa.CreateCredential(u, p.sd, parsed)
	if err != nil {
		a.log.Warn("auth: registration verify failed", "err", err)
		return Result{}, fmt.Errorf("%w: %v", ErrVerify, err)
	}
	var tr []string
	for _, t := range cred.Transport {
		tr = append(tr, string(t))
	}
	now := a.now()
	pk := store.Passkey{
		CredID: cred.ID, PublicKey: cred.PublicKey, SignCount: cred.Authenticator.SignCount, AAGUID: cred.Authenticator.AAGUID,
		Transports: strings.Join(tr, ","), BackupEligible: cred.Flags.BackupEligible, BackupState: cred.Flags.BackupState,
		UserVerified: cred.Flags.UserVerified, AttestationType: cred.AttestationType, Label: p.label, CreatedAt: now.Unix(),
	}
	cookie, ch, err := newSessionValue()
	if err != nil {
		return Result{}, err
	}
	exp := now.Add(SessionTTL)
	revoked, err := a.db.Enroll(ctx, th, pk, store.NewSession{TokenHash: ch, CredID: cred.ID, ExpiresAt: exp, UserAgent: trimUA(userAgent)}, now)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return Result{}, ErrBadToken
	case errors.Is(err, store.ErrConflict):
		return Result{}, ErrDuplicate
	case err != nil:
		return Result{}, err
	}
	a.log.Info("auth: passkey registered", "label", p.label, "revoked_existing", revoked)
	return Result{Passkey: pk, Cookie: cookie, ExpiresAt: exp, Revoked: revoked}, nil
}

// --- 로그인 ---

// BeginLogin은 discoverable credential 로그인 옵션을 만든다(allowCredentials 비움).
func (a *Auth) BeginLogin(ctx context.Context) (*protocol.CredentialAssertion, error) {
	ca, sd, err := a.wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, err
	}
	if err := a.put(&pending{sd: *sd, exp: a.now().Add(ChallengeTTL)}); err != nil {
		return nil, err
	}
	return ca, nil
}

// FinishLogin은 로그인 응답을 검증하고 세션을 만든다.
func (a *Auth) FinishLogin(ctx context.Context, credJSON []byte, userAgent string) (Result, error) {
	parsed, err := protocol.ParseCredentialRequestResponseBytes(credJSON)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrVerify, err)
	}
	p, err := a.take(challengeOf(parsed.Response.CollectedClientData), false)
	if err != nil {
		return Result{}, err
	}
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		var owner store.User
		var err error
		switch {
		case len(userHandle) > 0:
			owner, err = a.db.UserByHandle(ctx, userHandle)
		default:
			// Android Credential Manager는 discoverable 로그인에서도 userHandle을 비울 수 있다.
			a.log.Info("auth: login without user handle, looking up credential")
			owner, err = a.db.UserByCredID(ctx, rawID)
		}
		if err != nil {
			return nil, err
		}
		return a.loadUser(ctx, owner)
	}
	_, cred, err := a.wa.ValidatePasskeyLogin(handler, p.sd, parsed)
	if err != nil {
		a.log.Warn("auth: login verify failed", "err", err)
		return Result{}, fmt.Errorf("%w: %v", ErrVerify, err)
	}
	now := a.now()
	if cred.Authenticator.CloneWarning {
		// iCloud 키체인 패스키는 카운터가 늘 0이라 막지 않고 기록만 한다(설계 §2.8)
		a.log.Warn("auth: sign counter did not increase", "sign_count", cred.Authenticator.SignCount)
	}
	if err := a.db.UpdatePasskeyUse(ctx, cred.ID, cred.Authenticator.SignCount, cred.Flags.BackupState, cred.Authenticator.CloneWarning, now); err != nil {
		return Result{}, err
	}
	cookie, ch, err := newSessionValue()
	if err != nil {
		return Result{}, err
	}
	exp := now.Add(SessionTTL)
	owner, err := a.db.UserByCredID(ctx, cred.ID)
	if err != nil {
		return Result{}, err
	}
	if err := a.db.CreateSession(ctx, store.NewSession{TokenHash: ch, CredID: cred.ID, UserID: owner.ID, ExpiresAt: exp, UserAgent: trimUA(userAgent)}, now); err != nil {
		return Result{}, err
	}
	return Result{Passkey: store.Passkey{CredID: cred.ID}, Cookie: cookie, ExpiresAt: exp}, nil
}

func trimUA(s string) string {
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// --- 세션 ---

func newSessionValue() (value string, hash []byte, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", nil, err
	}
	v := base64.RawURLEncoding.EncodeToString(b[:])
	h := sha256.Sum256([]byte(v))
	return v, h[:], nil
}

func hashCookie(v string) []byte {
	h := sha256.Sum256([]byte(v))
	return h[:]
}

// CookieName은 세션 쿠키 이름이다. https면 __Host- 접두어(Secure·Path=/·Domain 없음 강제).
func (a *Auth) CookieName() string {
	if a.secure {
		return "__Host-mw_session"
	}
	return "mw_session"
}

// SetCookie는 세션 쿠키를 넣는다. HttpOnly, SameSite=Lax, (https면) Secure.
func (a *Auth) SetCookie(w http.ResponseWriter, value string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: a.CookieName(), Value: value, Path: "/", Expires: exp.UTC(), MaxAge: int(exp.Sub(a.now()).Seconds()),
		HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie는 세션 쿠키를 지운다.
func (a *Auth) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: a.CookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode})
}

// Session은 요청의 세션이다. 없거나 만료면 ok=false. 마지막 연장 뒤 하루가 지났으면 만료를 늘리고 쿠키를 다시 넣는다.
func (a *Auth) Session(w http.ResponseWriter, r *http.Request) (store.Session, bool) {
	c, err := r.Cookie(a.CookieName())
	if err != nil || c.Value == "" || len(c.Value) > 100 {
		return store.Session{}, false
	}
	now := a.now()
	h := hashCookie(c.Value)
	s, err := a.db.SessionByHash(r.Context(), h, now)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			a.log.Error("auth: session lookup", "err", err)
		}
		return store.Session{}, false
	}
	if now.Unix()-s.LastSeenAt >= int64(SessionRefresh/time.Second) && w != nil {
		exp := now.Add(SessionTTL)
		if err := a.db.TouchSession(r.Context(), h, exp, now); err != nil {
			a.log.Warn("auth: session refresh", "err", err)
		} else {
			s.ExpiresAt, s.LastSeenAt = exp.Unix(), now.Unix()
			a.SetCookie(w, c.Value, exp)
		}
	}
	return s, true
}

// Logout은 요청의 세션 행을 지우고 쿠키를 만료시킨다.
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(a.CookieName()); err == nil && c.Value != "" {
		if err := a.db.DeleteSession(r.Context(), hashCookie(c.Value)); err != nil {
			return err
		}
	}
	a.ClearCookie(w)
	return nil
}

// CheckOrigin은 상태를 바꾸는 요청의 Origin이 설정값과 같은지다. Origin이 없으면 거부한다.
func (a *Auth) CheckOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	return o != "" && strings.EqualFold(strings.TrimRight(o, "/"), a.origin)
}

// Cleanup은 만료 세션과 오래된 토큰을 지운다.
func (a *Auth) Cleanup(ctx context.Context) {
	s, t, err := a.db.CleanupAuth(ctx, a.now())
	if err != nil {
		a.log.Error("auth: cleanup", "err", err)
		return
	}
	if s+t > 0 {
		a.log.Info("auth: cleanup", "sessions", s, "tokens", t)
	}
}

// RunCleanup은 매일 04:30(KST)에 Cleanup을 돌린다. 시작할 때도 한 번 돌린다.
func (a *Auth) RunCleanup(ctx context.Context) {
	a.Cleanup(ctx)
	for {
		now := a.now()
		next := time.Date(now.Year(), now.Month(), now.Day(), 4, 30, 0, 0, kst.Loc)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		t := time.NewTimer(next.Sub(now))
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
			a.Cleanup(ctx)
		}
	}
}

// PasskeyID는 응답·경로에 쓰는 패스키 id(cred_id의 base64url)다.
func PasskeyID(credID []byte) string { return base64.RawURLEncoding.EncodeToString(credID) }

// ParsePasskeyID는 PasskeyID의 역이다.
func ParsePasskeyID(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }
