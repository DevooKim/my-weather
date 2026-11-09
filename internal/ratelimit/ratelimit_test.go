package ratelimit

import (
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func TestBucket(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	l := New(60, time.Minute, 20, c.now) // 분당 60, 순간 20
	for i := 0; i < 20; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("burst %d", i)
		}
	}
	ok, wait := l.Allow("a")
	if ok || wait <= 0 || wait > time.Second {
		t.Fatalf("21st: %v %v", ok, wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("other key")
	}
	c.t = c.t.Add(time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("refill")
	}
	// 10분 넘게 안 쓰면 지움
	c.t = c.t.Add(11 * time.Minute)
	l.Allow("c")
	if l.Len() != 1 {
		t.Fatalf("gc: %d", l.Len())
	}
}

func TestAllowAllDailyKeepsState(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	hour := New(10, time.Hour, 10, c.now)
	day := New(30, 24*time.Hour, 30, c.now)
	n := 0
	for h := 0; h < 5; h++ {
		for i := 0; i < 15; i++ {
			if ok, _ := AllowAll("ip", 1, hour, day); ok {
				n++
			}
		}
		c.t = c.t.Add(time.Hour)
	}
	// 토큰 버킷: 처음 30 + 4시간 동안 1.25개/시 = 35
	if n != 35 {
		t.Fatalf("allowed %d in 5h, want 35", n)
	}
	// 하루 버킷은 idle GC(최소 10분)에 지워지지 않아야 함: 마지막 사용 3시간 뒤에도 3.75개만 생김
	c.t = c.t.Add(2 * time.Hour)
	n = 0
	for i := 0; i < 10; i++ {
		if ok, _ := AllowAll("ip", 1, hour, day); ok {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("after idle: %d (daily bucket reset by gc?)", n)
	}
	// cost 2 (예산 tight: 한도 절반)
	if ok, _ := AllowAll("other", 2, hour, day); !ok {
		t.Fatal("cost 2")
	}
}

func TestClientIP(t *testing.T) {
	c := &ClientIP{TrustHeader: "X-Forwarded-For"}
	req := func(remote string, xff ...string) (string, string) {
		r := httptest.NewRequest("GET", "/api/timeline", nil)
		r.RemoteAddr = remote
		for _, v := range xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		return c.Resolve(r)
	}
	cases := []struct {
		remote string
		xff    []string
		ip     string
		key    string
	}{
		{"127.0.0.1:5555", []string{"203.0.113.9"}, "203.0.113.9", "203.0.113.9"},
		{"[::1]:5555", []string{"2001:db8:1:2:3:4:5:6"}, "2001:db8:1:2:3:4:5:6", "2001:db8:1:2::/64"},
		{"127.0.0.1:5555", []string{"::ffff:198.51.100.7"}, "198.51.100.7", "198.51.100.7"},
		{"127.0.0.1:5555", nil, "127.0.0.1", KeyLocal},
		// 오른쪽부터 처음 만나는 믿지 않는 주소 (왼쪽 값은 위조 가능)
		{"127.0.0.1:5555", []string{"1.2.3.4, 5.6.7.8"}, "5.6.7.8", "5.6.7.8"},
		{"127.0.0.1:5555", []string{"1.2.3.4", "5.6.7.8"}, "5.6.7.8", "5.6.7.8"},
		{"127.0.0.1:5555", []string{"1.2.3.4, 127.0.0.1"}, "1.2.3.4", "1.2.3.4"},
		{"127.0.0.1:5555", []string{"garbage"}, "127.0.0.1", KeyProxyUnknown},
		{"127.0.0.1:5555", []string{"9.9.9.9, garbage"}, "127.0.0.1", KeyProxyUnknown},
		// loopback이 아니면 헤더를 믿지 않음
		{"192.0.2.1:1234", []string{"203.0.113.9"}, "192.0.2.1", "192.0.2.1"},
		{"[2001:db8::1]:1234", nil, "2001:db8::1", "2001:db8::/64"},
		{"[::ffff:192.0.2.5]:1234", nil, "192.0.2.5", "192.0.2.5"},
	}
	for _, tc := range cases {
		ip, key := req(tc.remote, tc.xff...)
		if ip != tc.ip || key != tc.key {
			t.Errorf("%s %v: got (%s, %s) want (%s, %s)", tc.remote, tc.xff, ip, key, tc.ip, tc.key)
		}
	}
	// 헤더를 믿지 않도록 설정하면 loopback은 local
	c2 := &ClientIP{}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	if _, key := c2.Resolve(r); key != KeyLocal {
		t.Fatalf("no trust: %s", key)
	}
	// 프록시 두 단(사설 IP) → app
	ps, err := ParsePrefixes("127.0.0.0/8, ::1/128, 10.89.0.0/16, 172.16.0.0/12")
	if err != nil || len(ps) != 4 {
		t.Fatal(ps, err)
	}
	c3 := &ClientIP{TrustHeader: "X-Forwarded-For", Trusted: ps}
	r = httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.89.0.5:3333"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9, 127.0.0.1")
	if ip, key := c3.Resolve(r); ip != "203.0.113.9" || key != "203.0.113.9" {
		t.Fatalf("proxy chain: %s %s", ip, key)
	}
	r.Header.Del("X-Forwarded-For")
	if _, key := c3.Resolve(r); key != "10.89.0.5" {
		t.Fatalf("trusted proxy without header: %s", key)
	}
	// 사설 대역이 기본값에 없으면 헤더를 믿지 않음
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	if _, key := c.Resolve(r); key != "10.89.0.5" {
		t.Fatalf("untrusted proxy: %s", key)
	}
	// loopback을 믿는 범위에서 뺐는데 같은 netns 프록시가 헤더를 달고 오면 local(무제한)이 아님
	c4 := &ClientIP{TrustHeader: "X-Forwarded-For", Trusted: ps[2:3]}
	r = httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	if _, key := c4.Resolve(r); key != "127.0.0.1" {
		t.Fatalf("untrusted loopback with header: %s", key)
	}
	r.Header.Del("X-Forwarded-For")
	if _, key := c4.Resolve(r); key != KeyLocal {
		t.Fatalf("loopback without header: %s", key)
	}
	if _, err := ParsePrefixes("10.0.0.0/33"); err == nil {
		t.Fatal("bad cidr")
	}
	if ps, _ := ParsePrefixes("::ffff:10.0.0.0/104, 192.0.2.1"); ps[0].String() != "10.0.0.0/8" || ps[1].Bits() != 32 {
		t.Fatalf("%v", ps)
	}
	if BucketKey(netip.MustParseAddr("2001:db8:aa:bb:1::1")) != "2001:db8:aa:bb::/64" {
		t.Fatal("v6 /64")
	}
}
