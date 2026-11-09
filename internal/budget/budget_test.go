package budget

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"myweather/internal/kst"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestPerOpSeparate(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, kst.Loc)
	b := New(100, func() time.Time { return now }, quiet)
	for i := 0; i < 99; i++ {
		if !b.Reserve("getUltraSrtNcst", Favorite) {
			t.Fatalf("call %d blocked", i)
		}
	}
	// 99% 도달: 실황은 hard, 다른 오퍼레이션은 영향 없음
	if b.Level("getUltraSrtNcst") != Hard || b.Reserve("getUltraSrtNcst", Favorite) {
		t.Fatal("ncst should be hard")
	}
	if b.Level("getVilageFcst") != Normal || !b.Reserve("getVilageFcst", OnDemand3) {
		t.Fatal("vilage should be independent")
	}
	s := b.Snapshot()
	if s.ByOp["getUltraSrtNcst"] != 99 || s.ByOp["getVilageFcst"] != 1 || s.Levels["getUltraSrtNcst"] != "hard" {
		t.Fatalf("%+v", s)
	}
}

func TestStagesByLevel(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, kst.Loc)
	b := New(100, func() time.Time { return now }, quiet)
	op := "getVilageFcst"
	b.Restore(20261109, map[string]int{op: 60})
	if b.Level(op) != Tight || b.Reserve(op, OnDemand3) || !b.Reserve(op, OnDemand2) {
		t.Fatal("tight")
	}
	b.Restore(20261109, map[string]int{op: 80})
	if b.Level(op) != Degraded || b.Reserve(op, OnDemand2) || !b.Reserve(op, OnDemand1) {
		t.Fatal("degraded")
	}
	b.Restore(20261109, map[string]int{op: 92})
	if b.Level(op) != Reserve || b.Reserve(op, OnDemand1) || !b.Reserve(op, Favorite) {
		t.Fatal("reserve")
	}
}

func TestRolloverAtKSTMidnight(t *testing.T) {
	now := time.Date(2026, 11, 9, 23, 59, 0, 0, kst.Loc)
	b := New(10, func() time.Time { return now }, quiet)
	for i := 0; i < 10; i++ {
		b.Reserve("getUltraSrtFcst", Favorite)
	}
	if b.Reserve("getUltraSrtFcst", Favorite) {
		t.Fatal("should be exhausted")
	}
	now = time.Date(2026, 11, 10, 0, 0, 0, 0, kst.Loc)
	if !b.Reserve("getUltraSrtFcst", Favorite) {
		t.Fatal("should reset at KST midnight")
	}
	// 어제 날짜로 복구하려 하면 무시
	b.Restore(20261109, map[string]int{"getUltraSrtFcst": 10})
	if b.Snapshot().ByOp["getUltraSrtFcst"] != 1 {
		t.Fatal("stale restore applied")
	}
}
