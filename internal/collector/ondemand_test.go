package collector

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"myweather/internal/config"
	"myweather/internal/kma"
	"myweather/internal/kma/kmatest"
	"myweather/internal/kst"
)

var gC = config.Grid{NX: 60, NY: 127} // 수집 대상이 아닌 격자

func (e *env) odCollector(backfill, past int) *Collector {
	return New(e.db, e.api, e.bud, Options{
		Grids: []config.Grid{gA}, Log: quiet,
		Now:              func() time.Time { return e.now },
		OnDemandBackfill: backfill, OnDemandPast: past,
	})
}

func reqKeys(rs []kmatest.Req) map[string]int {
	m := map[string]int{}
	for _, r := range rs {
		m[fmt.Sprintf("%s/%d,%d/%d", r.Op, r.NX, r.NY, r.Base)]++
	}
	return m
}

func TestOnDemandStages(t *testing.T) {
	e := newEnv(t, 10000)
	e.now = at("2026-11-10 14:20")
	c := e.odCollector(2, 3)
	ctx := context.Background()

	if isNew, err := c.IsNewGrid(ctx, gC.NX, gC.NY); err != nil || !isNew {
		t.Fatalf("IsNewGrid=%v %v", isNew, err)
	}
	res, err := c.Ensure(ctx, gC.NX, gC.NY)
	if err != nil {
		t.Fatal(err)
	}
	c.WaitBackground()
	got := reqKeys(e.srv.Requests())
	want := []string{
		// 1단계
		"getVilageFcst/60,127/202611101400", "getUltraSrtFcst/60,127/202611101330",
		"getUltraSrtNcst/60,127/202611101400", "getVilageFcst/60,127/202611082300",
		// 2단계 (백필 2개)
		"getUltraSrtNcst/60,127/202611101300", "getUltraSrtNcst/60,127/202611101200",
		// 3단계 (Q2 b)
		"getVilageFcst/60,127/202611091100", "getVilageFcst/60,127/202611092300", "getVilageFcst/60,127/202611101100",
	}
	if len(got) != len(want) {
		t.Fatalf("requests %v", got)
	}
	for _, k := range want {
		if got[k] != 1 {
			t.Fatalf("missing %s in %v", k, got)
		}
	}
	if res.Complete || res.RetryAfter != retryAfterBg {
		t.Fatalf("first ensure %+v (background running)", res)
	}
	for _, r := range e.calls() {
		if r.trigger != TrigOnDemand || r.code != "00" {
			t.Fatalf("call %+v", r)
		}
	}
	// 같은 시간에 다시: 호출 없음, complete
	e.srv.Reset()
	res, _ = c.Ensure(ctx, gC.NX, gC.NY)
	if !res.Complete || len(e.srv.Requests()) != 0 {
		t.Fatalf("second ensure %+v reqs %d", res, len(e.srv.Requests()))
	}
	if isNew, _ := c.IsNewGrid(ctx, gC.NX, gC.NY); isNew {
		t.Fatal("seen grid is not new")
	}
	// 다음 시간: 새 실황·초단기 + 아직 빈 실황 백필(상한 2개: 11시·10시). 단기는 14시 발표가 최신이라 없음
	e.now = at("2026-11-10 15:50")
	c.Ensure(ctx, gC.NX, gC.NY)
	c.WaitBackground()
	got = reqKeys(e.srv.Requests())
	if len(got) != 4 || got["getUltraSrtNcst/60,127/202611101500"] != 1 || got["getUltraSrtFcst/60,127/202611101530"] != 1 ||
		got["getUltraSrtNcst/60,127/202611101100"] != 1 || got["getUltraSrtNcst/60,127/202611101000"] != 1 {
		t.Fatalf("next hour %v", got)
	}
	// 수집 대상 격자는 즉시 조회하지 않음
	e.srv.Reset()
	if res, _ := c.Ensure(ctx, gA.NX, gA.NY); !res.Complete || len(e.srv.Requests()) != 0 {
		t.Fatal("collected grid must not call")
	}
	if isNew, _ := c.IsNewGrid(ctx, gA.NX, gA.NY); isNew {
		t.Fatal("collected grid is not new")
	}
}

func TestOnDemandSingleflight(t *testing.T) {
	e := newEnv(t, 10000)
	e.now = at("2026-11-10 14:20")
	release := make(chan struct{})
	e.srv.SetResponder(func(r kmatest.Req) (int, string) {
		<-release
		return kmatest.OK(r)
	})
	c := e.odCollector(0, 0)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Ensure(context.Background(), gC.NX, gC.NY)
		}()
	}
	time.Sleep(200 * time.Millisecond)
	close(release)
	wg.Wait()
	c.WaitBackground()
	for k, n := range reqKeys(e.srv.Requests()) {
		if n != 1 {
			t.Fatalf("%s requested %d times", k, n)
		}
	}
	if n := len(e.srv.Requests()); n != 4 {
		t.Fatalf("requests %d", n)
	}
}

func TestOnDemandFallbackAndNegativeCache(t *testing.T) {
	e := newEnv(t, 10000)
	e.now = at("2026-11-10 14:20")
	e.srv.SetResponder(func(r kmatest.Req) (int, string) {
		if r.Op == string(kma.OpVilage) && r.Base == 202611101400 {
			return 200, kmatest.NoData
		}
		return kmatest.OK(r)
	})
	c := e.odCollector(0, 0)
	ctx := context.Background()
	c.Ensure(ctx, gC.NX, gC.NY)
	got := reqKeys(e.srv.Requests())
	if got["getVilageFcst/60,127/202611101400"] != 1 || got["getVilageFcst/60,127/202611101100"] != 1 || got["getVilageFcst/60,127/202611100800"] != 0 {
		t.Fatalf("fallback %v", got)
	}
	// 1분 뒤 다시: 실패한 14시는 부르지 않음(FailTTL), 11시는 이미 있음
	e.srv.Reset()
	e.now = e.now.Add(time.Minute)
	c.Ensure(ctx, gC.NX, gC.NY)
	if n := len(e.srv.Requests()); n != 0 {
		t.Fatalf("negative cache: %v", reqKeys(e.srv.Requests()))
	}
	// FailTTL 뒤에는 다시 부름
	e.now = e.now.Add(FailTTL)
	c.Ensure(ctx, gC.NX, gC.NY)
	if got := reqKeys(e.srv.Requests()); got["getVilageFcst/60,127/202611101400"] != 1 {
		t.Fatalf("after ttl %v", got)
	}
}

func TestOnDemandBudgetBlocked(t *testing.T) {
	e := newEnv(t, 100)
	e.now = at("2026-11-10 14:20")
	e.bud.Restore(kst.Day(e.now), map[string]int{string(kma.OpVilage): 95, string(kma.OpUltra): 95, string(kma.OpNcst): 95})
	c := e.odCollector(2, 3)
	res, _ := c.Ensure(context.Background(), gC.NX, gC.NY)
	c.WaitBackground()
	if !res.Blocked || !res.Degraded || len(e.srv.Requests()) != 0 {
		t.Fatalf("%+v reqs %d", res, len(e.srv.Requests()))
	}
}
