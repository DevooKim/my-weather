// Package budget은 KMA 하루 호출 예산을 오퍼레이션(상세기능)별로 따로 집계하고 단계별로 제한한다.
//
// data.go.kr 한도는 상세기능별 하루 10,000건씩이다(설계 §8.1 Q5). 그래서 §6.3의 단계표를
// 전역이 아니라 오퍼레이션마다 적용한다. 카운터는 KST 자정에 0으로 돌아간다.
package budget

import (
	"log/slog"
	"sync"
	"time"

	"myweather/internal/kst"
)

// Level은 예산 단계다(설계 §6.3).
type Level int

const (
	Normal   Level = iota // < 60%
	Tight                 // 60~80%
	Degraded              // 80~92%
	Reserve               // 92~99%
	Hard                  // ≥ 99%
)

func (l Level) String() string {
	return [...]string{"normal", "tight", "degraded", "reserve", "hard"}[l]
}

// Stage는 호출의 용도다. 단계가 올라갈수록 덜 중요한 용도부터 막는다.
type Stage int

const (
	Favorite  Stage = iota // 즐겨찾기(설정 격자) 정기 수집: hard 전까지 허용
	OnDemand1              // 즉시 조회 1단계: reserve부터 차단
	OnDemand2              // 즉시 조회 2단계(실황 백필): degraded부터 차단
	OnDemand3              // 즉시 조회 3단계(과거 추정 단기): tight부터 차단
)

func (s Stage) blockedAt() Level {
	switch s {
	case OnDemand1:
		return Reserve
	case OnDemand2:
		return Degraded
	case OnDemand3:
		return Tight
	default:
		return Hard
	}
}

// Budget은 오퍼레이션별 하루 호출 카운터다. 동시에 써도 안전하다.
type Budget struct {
	mu     sync.Mutex
	limit  int
	now    func() time.Time
	day    int64
	counts map[string]int
	levels map[string]Level
	log    *slog.Logger
}

// New는 오퍼레이션마다 하루 limit건인 예산을 만든다. now가 nil이면 kst.Now.
func New(limit int, now func() time.Time, log *slog.Logger) *Budget {
	if now == nil {
		now = kst.Now
	}
	if log == nil {
		log = slog.Default()
	}
	b := &Budget{limit: limit, now: now, counts: map[string]int{}, levels: map[string]Level{}, log: log}
	b.day = kst.Day(now())
	return b
}

// Restore는 시작할 때 DB에서 센 오늘 호출 수를 넣는다(설계 §4.2).
func (b *Budget) Restore(day int64, counts map[string]int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollover()
	if day != b.day {
		return
	}
	for op, n := range counts {
		b.counts[op] = n
		b.levels[op] = b.levelOf(n)
	}
}

func (b *Budget) rollover() {
	if d := kst.Day(b.now()); d != b.day {
		b.day = d
		b.counts = map[string]int{}
		b.levels = map[string]Level{}
	}
}

func (b *Budget) levelOf(n int) Level {
	if b.limit <= 0 {
		return Hard
	}
	switch p := n * 100; {
	case p >= b.limit*99:
		return Hard
	case p >= b.limit*92:
		return Reserve
	case p >= b.limit*80:
		return Degraded
	case p >= b.limit*60:
		return Tight
	}
	return Normal
}

// Reserve는 op 호출 하나를 허용할지 정하고, 허용하면 바로 센다(호출 전에 센다. 실패한 호출도 한도에 들어간다).
func (b *Budget) Reserve(op string, s Stage) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollover()
	n := b.counts[op]
	if b.levelOf(n) >= s.blockedAt() {
		return false
	}
	n++
	b.counts[op] = n
	if lv := b.levelOf(n); lv != b.levels[op] {
		b.log.Warn("kma budget level changed", "op", op, "from", b.levels[op].String(), "to", lv.String(), "calls", n, "limit", b.limit)
		b.levels[op] = lv
	}
	return true
}

// Level은 op의 현재 단계다.
func (b *Budget) Level(op string) Level {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollover()
	return b.levelOf(b.counts[op])
}

// Snapshot은 상태 보고용 사본이다.
type Snapshot struct {
	Day    int64
	Limit  int
	ByOp   map[string]int
	Levels map[string]string
}

// Snapshot은 오늘 오퍼레이션별 호출 수와 단계다.
func (b *Budget) Snapshot() Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rollover()
	s := Snapshot{Day: b.day, Limit: b.limit, ByOp: map[string]int{}, Levels: map[string]string{}}
	for op, n := range b.counts {
		s.ByOp[op] = n
		s.Levels[op] = b.levelOf(n).String()
	}
	return s
}
