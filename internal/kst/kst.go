// Package kst는 Asia/Seoul 고정 시계와 기상청 발표 시각(base) 계산을 담당한다.
//
// 모든 시각 계산은 컨테이너 TZ와 무관하게 Loc 기준으로 한다. tzdata는 바이너리에 포함된다.
package kst

import (
	"fmt"
	"time"
	_ "time/tzdata" // 컨테이너에 zoneinfo가 없어도 Asia/Seoul을 쓸 수 있게 한다.
)

// Loc는 Asia/Seoul 위치다.
var Loc = mustLoad()

func mustLoad() *time.Location {
	l, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		panic(err)
	}
	return l
}

// Now는 KST 현재 시각이다.
func Now() time.Time { return time.Now().In(Loc) }

// ToInt는 시각을 KST 정수 YYYYMMDDHHMM으로 바꾼다.
func ToInt(t time.Time) int64 {
	t = t.In(Loc)
	return int64(t.Year())*100000000 + int64(t.Month())*1000000 + int64(t.Day())*10000 + int64(t.Hour())*100 + int64(t.Minute())
}

// FromInt는 YYYYMMDDHHMM 정수를 KST 시각으로 바꾼다.
func FromInt(v int64) time.Time {
	mi := int(v % 100)
	h := int(v / 100 % 100)
	d := int(v / 10000 % 100)
	mo := int(v / 1000000 % 100)
	y := int(v / 100000000)
	return time.Date(y, time.Month(mo), d, h, mi, 0, 0, Loc)
}

// Day는 예산 집계용 KST 날짜 정수 YYYYMMDD다.
func Day(t time.Time) int64 { return ToInt(t) / 10000 }

// ParseBase는 응답의 baseDate("20261109")+baseTime("2000")를 정수로 바꾼다.
func ParseBase(date, hhmm string) (int64, error) {
	if len(date) != 8 || len(hhmm) != 4 {
		return 0, fmt.Errorf("bad date/time %q %q", date, hhmm)
	}
	var v int64
	for _, c := range date + hhmm {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("bad date/time %q %q", date, hhmm)
		}
		v = v*10 + int64(c-'0')
	}
	return v, nil
}

// Kind는 발표 주기의 종류다.
type Kind int

const (
	Ncst   Kind = iota // 초단기실황: 매시 hh:00, hh:10부터 조회
	Ultra              // 초단기예보: 매시 hh:30, hh:45부터 조회
	Vilage             // 단기예보: 02·05·…·23시, +10분부터 조회
)

// Delay는 발표 시각부터 첫 조회까지의 지연이다(설계 §5.1).
func (k Kind) Delay() time.Duration {
	switch k {
	case Ultra:
		return 15 * time.Minute
	default:
		return 10 * time.Minute
	}
}

// BaseAtOrBefore는 시각 t 이전(같은 시각 포함)의 가장 늦은 발표 시각이다. 조회 가능 여부는 따지지 않는다.
func (k Kind) BaseAtOrBefore(t time.Time) time.Time {
	t = t.In(Loc)
	h := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, Loc)
	switch k {
	case Ultra:
		b := h.Add(30 * time.Minute)
		if b.After(t) {
			b = b.Add(-time.Hour)
		}
		return b
	case Vilage:
		// 02,05,...,23시: (hour+1)%3 == 0
		for (h.Hour()+1)%3 != 0 {
			h = h.Add(-time.Hour)
		}
		return h
	default:
		return h
	}
}

// Latest는 now 시점에 조회할 수 있는 최신 발표 시각이다(발표 + Delay ≤ now).
func (k Kind) Latest(now time.Time) time.Time { return k.BaseAtOrBefore(now.Add(-k.Delay())) }

// Step은 발표 간격이다.
func (k Kind) Step() time.Duration {
	if k == Vilage {
		return 3 * time.Hour
	}
	return time.Hour
}

// Next는 base 다음 발표 시각이다.
func (k Kind) Next(base time.Time) time.Time { return base.Add(k.Step()) }

// Prev는 base 직전 발표 시각이다.
func (k Kind) Prev(base time.Time) time.Time { return base.Add(-k.Step()) }

// FirstFetch는 base의 첫 조회 시각이다.
func (k Kind) FirstFetch(base time.Time) time.Time { return base.Add(k.Delay()) }
