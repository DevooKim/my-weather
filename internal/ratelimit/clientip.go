package ratelimit

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
)

// 특수 버킷 키
const (
	KeyLocal        = "local"         // loopback에서 전달 헤더 없이 온 요청(헬스체크, 서버 안 호출). 제한하지 않음
	KeyProxyUnknown = "proxy-unknown" // 믿는 프록시에서 왔는데 전달 헤더 형식이 이상함
)

// DefaultTrustedProxies는 MYWEATHER_TRUSTED_PROXIES 기본값이다.
const DefaultTrustedProxies = "127.0.0.0/8,::1/128"

// ParsePrefixes는 쉼표로 구분한 CIDR 목록을 읽는다. 맨 IP는 /32·/128로 본다.
func ParsePrefixes(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			a, err := netip.ParseAddr(part)
			if err != nil {
				return nil, fmt.Errorf("bad proxy address %q", part)
			}
			a = a.Unmap()
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("bad proxy CIDR %q", part)
		}
		if p.Addr().Is4In6() {
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// ClientIP는 요청의 클라이언트 IP와 버킷 키를 정한다(앞단 리버스 프록시가 X-Forwarded-For를 붙이는 구성).
//
//   - RemoteAddr가 Trusted(기본 loopback) 안이고 TrustHeader가 있을 때만 그 헤더를 본다.
//     헤더 값(여러 줄이면 이어 붙임)을 쉼표로 나눠 오른쪽부터 보며, Trusted 안의 주소는 건너뛰고
//     처음 만나는 믿지 않는 주소를 클라이언트 IP로 쓴다(앞쪽 값은 클라이언트가 위조할 수 있으므로 보지 않음).
//   - 헤더가 없으면: loopback이면 KeyLocal, 아니면 그 프록시 IP 버킷.
//   - 값이 IP로 읽히지 않으면 KeyProxyUnknown 한 버킷(경고 1회). 모두 믿는 주소면 가장 왼쪽 값.
//   - 그 밖에는 RemoteAddr. 전달 헤더 없이 loopback에서 온 요청만 KeyLocal(헤더가 있으면 loopback IP 버킷).
//   - 버킷 키: IPv4 /32, IPv6 /64. IPv4-mapped IPv6는 IPv4로 본다.
type ClientIP struct {
	TrustHeader string         // 예: "X-Forwarded-For". 비우면 헤더를 믿지 않음
	Trusted     []netip.Prefix // nil이면 DefaultTrustedProxies
	Log         *slog.Logger
	warnOnce    sync.Once
}

var defaultTrusted, _ = ParsePrefixes(DefaultTrustedProxies)

func (c *ClientIP) trusted(a netip.Addr) bool {
	ps := c.Trusted
	if ps == nil {
		ps = defaultTrusted
	}
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Resolve는 (로그용 IP 문자열, 버킷 키)를 돌려준다.
func (c *ClientIP) Resolve(r *http.Request) (ip, key string) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host, "peer:" + host
	}
	peer = peer.Unmap()
	if c.TrustHeader != "" && c.trusted(peer) {
		vs := r.Header.Values(c.TrustHeader)
		if len(vs) == 0 {
			if peer.IsLoopback() {
				return peer.String(), KeyLocal
			}
			return peer.String(), BucketKey(peer)
		}
		parts := strings.Split(strings.Join(vs, ","), ",")
		var last netip.Addr
		for i := len(parts) - 1; i >= 0; i-- {
			a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
			if err != nil {
				c.warn(len(parts))
				return peer.String(), KeyProxyUnknown
			}
			a = a.Unmap()
			last = a
			if !c.trusted(a) {
				return a.String(), BucketKey(a)
			}
		}
		return last.String(), BucketKey(last)
	}
	// 믿지 않는 loopback 피어가 전달 헤더를 달고 오면(같은 netns의 프록시인데 범위에 안 넣은 경우) local로 보지 않는다.
	if peer.IsLoopback() && (c.TrustHeader == "" || len(r.Header.Values(c.TrustHeader)) == 0) {
		return peer.String(), KeyLocal
	}
	return peer.String(), BucketKey(peer)
}

func (c *ClientIP) warn(n int) {
	c.warnOnce.Do(func() {
		if c.Log != nil {
			c.Log.Warn("ratelimit: malformed forwarded header from trusted proxy; using one shared bucket", "header", c.TrustHeader, "entries", n)
		}
	})
}

// BucketKey는 IPv4 /32, IPv6 /64 키다.
func BucketKey(a netip.Addr) string {
	a = a.Unmap()
	if a.Is4() {
		return a.String()
	}
	p, err := a.Prefix(64)
	if err != nil {
		return a.String()
	}
	return p.String()
}
