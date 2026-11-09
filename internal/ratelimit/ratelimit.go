// Package ratelimit은 IP(버킷 키)별 토큰 버킷 요청 제한과 클라이언트 IP 판정을 담당한다(설계 §6.2).
//
// 버킷은 메모리 맵이다. 가득 찰 때까지 걸리는 시간보다 오래 쓰지 않은 항목은 지운다
// (최소 10분. 하루 한도 버킷은 하루 동안 남는다).
package ratelimit

import (
	"math"
	"sync"
	"time"
)

// Limiter는 키별 토큰 버킷이다. 동시에 써도 안전하다.
type Limiter struct {
	mu     sync.Mutex
	rate   float64 // 초당 채워지는 토큰
	burst  float64
	idle   time.Duration
	now    func() time.Time
	m      map[string]*bucket
	lastGC time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New는 period마다 n개가 채워지고 최대 burst개까지 쌓이는 제한을 만든다. now가 nil이면 time.Now.
func New(n int, period time.Duration, burst int, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	rate := float64(n) / period.Seconds()
	idle := time.Duration(float64(burst)/rate*float64(time.Second)) + time.Minute
	if idle < 10*time.Minute {
		idle = 10 * time.Minute
	}
	return &Limiter{rate: rate, burst: float64(burst), idle: idle, now: now, m: map[string]*bucket{}}
}

func (l *Limiter) refill(key string, now time.Time) *bucket {
	b := l.m[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.m[key] = b
		return b
	}
	if el := now.Sub(b.last).Seconds(); el > 0 {
		b.tokens = math.Min(l.burst, b.tokens+el*l.rate)
		b.last = now
	}
	return b
}

func (l *Limiter) gc(now time.Time) {
	if now.Sub(l.lastGC) < time.Minute {
		return
	}
	l.lastGC = now
	for k, b := range l.m {
		if now.Sub(b.last) > l.idle {
			delete(l.m, k)
		}
	}
}

// wait는 cost개가 모일 때까지의 시간이다(0이면 지금 가능).
func (l *Limiter) wait(b *bucket, cost float64) time.Duration {
	if b.tokens >= cost {
		return 0
	}
	return time.Duration((cost - b.tokens) / l.rate * float64(time.Second))
}

// Allow는 key에서 토큰 1개를 쓴다. 안 되면 다시 시도할 때까지의 시간을 돌려준다.
func (l *Limiter) Allow(key string) (bool, time.Duration) { return AllowAll(key, 1, l) }

var allMu sync.Mutex

// AllowAll은 모든 제한에서 cost개를 한꺼번에 쓴다. 하나라도 모자라면 아무것도 쓰지 않는다.
func AllowAll(key string, cost int, ls ...*Limiter) (bool, time.Duration) {
	allMu.Lock()
	defer allMu.Unlock()
	c := float64(cost)
	var worst time.Duration
	for _, l := range ls {
		l.mu.Lock()
		now := l.now()
		l.gc(now)
		if w := l.wait(l.refill(key, now), c); w > worst {
			worst = w
		}
		l.mu.Unlock()
	}
	if worst > 0 {
		return false, worst
	}
	for _, l := range ls {
		l.mu.Lock()
		l.m[key].tokens -= c
		l.mu.Unlock()
	}
	return true, 0
}

// Len은 버킷 수다(테스트용).
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.m)
}
