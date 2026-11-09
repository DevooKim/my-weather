// Package push는 Web Push(VAPID) 발송과 실패 구독 정리를 맡는다(설계 §2.7, §7).
//
//   - 푸시 서비스가 404·410을 돌려주면 그 구독을 바로 지운다. 다른 오류(네트워크 포함)가 연속 5회면 지운다.
//   - 본문은 {"title","body","url","tag"} JSON. 같은 tag면 iOS에서 알림이 쌓이지 않고 바뀐다.
package push

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"myweather/internal/kst"
	"myweather/internal/store"
)

// MaxFails는 404·410이 아닌 실패가 이만큼 이어지면 구독을 지우는 횟수다.
const MaxFails = 5

// Message는 알림 하나다.
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url,omitempty"`
	Tag   string `json:"tag,omitempty"`

	TTL     time.Duration `json:"-"` // 푸시 서비스 보관 시간
	Urgency string        `json:"-"` // very-low | low | normal | high
}

// Report는 발송 결과다.
type Report struct {
	Sent    int `json:"sent"`
	Failed  int `json:"failed"`
	Removed int `json:"removed"`
}

// Options는 발송 설정이다.
type Options struct {
	DB         *store.DB
	PublicKey  string // VAPID 공개키 (base64url, 65바이트 비압축 P-256)
	PrivateKey string // VAPID 개인키 (base64url, 32바이트)
	Subject    string // mailto:… 또는 https://…
	HTTPClient webpush.HTTPClient
	Now        func() time.Time
	Log        *slog.Logger
}

// Sender는 Web Push 발송기다.
type Sender struct {
	o Options
}

// New는 발송기를 만든다. 키 형식이 틀리면 오류.
func New(o Options) (*Sender, error) {
	if err := ValidateKeys(o.PublicKey, o.PrivateKey); err != nil {
		return nil, err
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if o.Now == nil {
		o.Now = kst.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return &Sender{o: o}, nil
}

// PublicKey는 브라우저 applicationServerKey로 쓸 공개키다.
func (s *Sender) PublicKey() string { return s.o.PublicKey }

func decodeKey(k string) ([]byte, error) {
	k = strings.TrimRight(strings.TrimSpace(k), "=")
	if b, err := base64.RawURLEncoding.DecodeString(k); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(k)
}

// ValidateKeys는 VAPID 키 쌍이 P-256이고 서로 맞는지 확인한다.
func ValidateKeys(pub, priv string) error {
	pb, err := decodeKey(pub)
	if err != nil || len(pb) != 65 || pb[0] != 4 {
		return errors.New("push: VAPID public key must be base64url uncompressed P-256 (65 bytes)")
	}
	kb, err := decodeKey(priv)
	if err != nil || len(kb) != 32 {
		return errors.New("push: VAPID private key must be base64url 32 bytes")
	}
	k, err := ecdh.P256().NewPrivateKey(kb)
	if err != nil {
		return fmt.Errorf("push: VAPID private key: %w", err)
	}
	if string(k.PublicKey().Bytes()) != string(pb) {
		return errors.New("push: VAPID public key does not match private key")
	}
	return nil
}

// GenerateKeys는 새 VAPID 키 쌍(base64url)을 만든다.
func GenerateKeys() (pub, priv string, err error) {
	priv, pub, err = webpush.GenerateVAPIDKeys()
	return pub, priv, err
}

// ValidateSubscription은 브라우저 구독의 endpoint와 키를 확인한다.
func ValidateSubscription(endpoint, p256dh, auth string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || len(endpoint) > 2048 {
		return errors.New("endpoint must be an https URL")
	}
	pb, err := decodeKey(p256dh)
	if err != nil || len(pb) != 65 || pb[0] != 4 {
		return errors.New("keys.p256dh must be an uncompressed P-256 public key")
	}
	if _, err := ecdh.P256().NewPublicKey(pb); err != nil {
		return errors.New("keys.p256dh is not on P-256")
	}
	ab, err := decodeKey(auth)
	if err != nil || len(ab) != 16 {
		return errors.New("keys.auth must be 16 bytes")
	}
	return nil
}

// subscriber는 webpush-go가 sub 클레임에 'mailto:'를 스스로 붙이므로(https:가 아니면) 접두어를 뗀 값이다.
func subscriber(subject string) string {
	return strings.TrimPrefix(subject, "mailto:")
}

// SendAll은 endpoint(비면 모든 구독)로 보낸다.
func (s *Sender) SendAll(ctx context.Context, m Message, endpoint string) (Report, error) {
	subs, err := s.o.DB.PushSubs(ctx, endpoint)
	if err != nil {
		return Report{}, err
	}
	var rep Report
	for _, sub := range subs {
		switch s.sendOne(ctx, m, sub) {
		case resultOK:
			rep.Sent++
		case resultRemoved:
			rep.Failed++
			rep.Removed++
		default:
			rep.Failed++
		}
	}
	return rep, nil
}

type result int

const (
	resultOK result = iota
	resultFailed
	resultRemoved
)

func (s *Sender) sendOne(ctx context.Context, m Message, sub store.PushSub) result {
	body, _ := json.Marshal(m)
	ttl := int(m.TTL / time.Second)
	if ttl <= 0 {
		ttl = 3600
	}
	urg := webpush.Urgency(m.Urgency)
	if urg == "" {
		urg = webpush.UrgencyNormal
	}
	host := sub.Endpoint
	if u, err := url.Parse(sub.Endpoint); err == nil {
		host = u.Host // 로그에 endpoint 전체(구독 비밀 경로)를 남기지 않는다
	}
	resp, err := webpush.SendNotificationWithContext(ctx, body, &webpush.Subscription{
		Endpoint: sub.Endpoint, Keys: webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
	}, &webpush.Options{
		HTTPClient: s.o.HTTPClient, Subscriber: subscriber(s.o.Subject), TTL: ttl, Urgency: urg,
		VAPIDPublicKey: s.o.PublicKey, VAPIDPrivateKey: s.o.PrivateKey,
	})
	status := 0
	if resp != nil {
		status = resp.StatusCode
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
	}
	now := s.o.Now()
	if err == nil && status >= 200 && status < 300 {
		if err := s.o.DB.PushOK(ctx, sub.Endpoint, now); err != nil {
			s.o.Log.Error("push: db", "err", err)
		}
		return resultOK
	}
	if status == http.StatusNotFound || status == http.StatusGone {
		s.o.Log.Info("push: subscription gone, removed", "host", host, "status", status, "id", sub.ID)
		if err := s.o.DB.DeletePushSub(ctx, sub.Endpoint); err != nil {
			s.o.Log.Error("push: db", "err", err)
		}
		return resultRemoved
	}
	n, dberr := s.o.DB.PushFail(ctx, sub.Endpoint)
	if dberr != nil {
		s.o.Log.Error("push: db", "err", dberr)
	}
	s.o.Log.Warn("push: send failed", "host", host, "status", status, "err", err, "fails", n, "id", sub.ID)
	if n >= MaxFails {
		if err := s.o.DB.DeletePushSub(ctx, sub.Endpoint); err != nil {
			s.o.Log.Error("push: db", "err", err)
		}
		s.o.Log.Info("push: subscription removed after repeated failures", "host", host, "id", sub.ID)
		return resultRemoved
	}
	return resultFailed
}
