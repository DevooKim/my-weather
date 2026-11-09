package collector

import (
	"context"
	"sync"
	"time"

	"myweather/internal/budget"
	"myweather/internal/config"
	"myweather/internal/kma"
	"myweather/internal/kst"
	"myweather/internal/store"
)

// 즉시 조회 (설계 §5.6). 수집 대상이 아닌 격자의 타임라인 요청이 오면 필요한 발표분을 받는다.
//
//   - 1단계(요청 안에서 최대 Stage1Wait 기다림): 최신 단기·초단기·실황, 그저께 23시 단기.
//     최신 단기·초단기가 실패하고 직전 발표분 2개가 모두 없으면 직전 발표분을 최대 2개까지 거슬러 받는다.
//   - 2단계(뒤에서): 지난 23시간 실황 백필(최대 OnDemandBackfill개).
//   - 3단계(뒤에서): 과거 칸 추정용 단기 발표분. Q2(b) 단순화: 어제 11시·어제 23시·오늘 11시 중
//     최신 발표분보다 이른 것(최대 OnDemandPast개). 그저께 23시는 1단계에서 받는다.
//
// 같은 (op, nx, ny, base)는 정기 수집과 즉시 조회를 통틀어 한 번만 보내고 결과를 나눈다(flight).
// 실패한 키는 FailTTL 동안 다시 부르지 않는다(NO_DATA인 발표분에 클라이언트 재요청이 몰려 예산을 쓰지 않게).
// 즉시 조회는 정기 수집 상태(jobs·refetch·backfill)를 건드리지 않는다.

// TrigOnDemand는 즉시 조회 호출의 kma_call.trigger다.
const TrigOnDemand = "ondemand"

// 즉시 조회 기본값
const (
	Stage1Wait     = 3 * time.Second
	FailTTL        = 2 * time.Minute
	touchEvery     = 10 * time.Minute
	retryAfterWait = 3 * time.Second // 1단계가 시간 안에 안 끝났을 때
	retryAfterBg   = 5 * time.Second // 2·3단계가 도는 중일 때
)

type gridKey struct{ nx, ny int }

type bgState struct {
	hour    int64 // 이 시(KST YYYYMMDDHH00)에 2·3단계를 시작했음
	running bool
}

type odState struct {
	mu      sync.Mutex
	failed  map[key]time.Time
	bg      map[gridKey]*bgState
	touched map[gridKey]time.Time
	wg      sync.WaitGroup
}

func newOdState() odState {
	return odState{failed: map[key]time.Time{}, bg: map[gridKey]*bgState{}, touched: map[gridKey]time.Time{}}
}

// Ensured는 즉시 조회 결과다.
type Ensured struct {
	Complete   bool          // 1단계가 끝났고 2·3단계가 돌고 있지 않음
	RetryAfter time.Duration // Complete=false일 때 다시 부를 때까지
	Degraded   bool          // 예산 단계 때문에 건너뛴 단계가 있음
	Blocked    bool          // 1단계 호출이 예산(reserve 이상)에 막힘
}

// IsCollected는 정기 수집 대상 격자인지다.
func (c *Collector) IsCollected(nx, ny int) bool {
	for _, g := range c.opt.Grids {
		if g.NX == nx && g.NY == ny {
			return true
		}
	}
	return false
}

// IsNewGrid는 즉시 조회로 처음 KMA를 부르게 되는 격자인지다(설계 §6.2 새 격자 IP 한도용).
// 수집 대상이거나 grid_seen에 있으면 false.
func (c *Collector) IsNewGrid(ctx context.Context, nx, ny int) (bool, error) {
	if c.IsCollected(nx, ny) {
		return false, nil
	}
	c.od.mu.Lock()
	_, touched := c.od.touched[gridKey{nx, ny}]
	c.od.mu.Unlock()
	if touched {
		return false, nil
	}
	seen, err := c.db.GridSeen(ctx, nx, ny)
	return !seen, err
}

// WaitBackground는 진행 중인 2·3단계가 끝날 때까지 기다린다(테스트·종료용).
func (c *Collector) WaitBackground() { c.od.wg.Wait() }

func (c *Collector) lifeCtx() context.Context {
	if c.opt.Context != nil {
		return c.opt.Context
	}
	return context.Background()
}

// Ensure는 수집 대상이 아닌 격자에 필요한 발표분을 받는다. 수집 대상이면 아무것도 하지 않는다.
// ctx는 기다리는 데만 쓴다. 호출 자체는 서버 수명 ctx로 하므로 요청이 끝나도 결과는 저장된다.
func (c *Collector) Ensure(ctx context.Context, nx, ny int) (Ensured, error) {
	if c.IsCollected(nx, ny) {
		return Ensured{Complete: true}, nil
	}
	now := c.opt.Now()
	gk := gridKey{nx, ny}
	c.od.mu.Lock()
	needTouch := now.Sub(c.od.touched[gk]) >= touchEvery
	if needTouch {
		c.od.touched[gk] = now
	}
	c.od.mu.Unlock()
	if needTouch {
		if err := c.db.TouchGrid(ctx, nx, ny, now); err != nil {
			return Ensured{}, err
		}
	}

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, kst.Loc)
	type job struct {
		op       kma.Op
		base     time.Time
		fallback bool
	}
	jobs := []job{
		{kma.OpVilage, kst.Vilage.Latest(now), true},
		{kma.OpUltra, kst.Ultra.Latest(now), true},
		{kma.OpNcst, kst.Ncst.Latest(now), false},
		{kma.OpVilage, today.Add(-25 * time.Hour), false}, // 그저께 23시: 과거 칸 전체의 최소 출처
	}
	var res Ensured
	var mu sync.Mutex
	done := make(chan struct{})
	var wg sync.WaitGroup
	for _, j := range jobs {
		k := key{j.op, nx, ny, kst.ToInt(j.base)}
		has, err := c.has(ctx, k)
		if err != nil {
			return Ensured{}, err
		}
		if has {
			continue
		}
		wg.Add(1)
		go func(j job, k key) {
			defer wg.Done()
			f, called := c.shared(k, budget.OnDemand1)
			if !called {
				mu.Lock()
				res.Blocked, res.Degraded = true, true
				mu.Unlock()
				return
			}
			if f == nil || f.ok || !j.fallback {
				return
			}
			// 최신 발표분 실패: 직전 발표분 2개가 모두 없으면 최근 것부터 하나씩(설계 §5.3)
			kd := j.op.Kind()
			p1 := key{j.op, nx, ny, kst.ToInt(kd.Prev(j.base))}
			p2 := key{j.op, nx, ny, kst.ToInt(kd.Prev(kd.Prev(j.base)))}
			for _, pk := range []key{p1, p2} {
				if has, err := c.has(c.lifeCtx(), pk); err != nil || has {
					return
				}
			}
			for _, pk := range []key{p1, p2} {
				if f, called := c.shared(pk, budget.OnDemand1); !called || (f != nil && f.ok) {
					return
				}
			}
		}(j, k)
	}
	go func() { wg.Wait(); close(done) }()

	timer := time.NewTimer(Stage1Wait)
	defer timer.Stop()
	timedOut := false
	select {
	case <-done:
	case <-timer.C:
		timedOut = true
	case <-ctx.Done():
		return Ensured{}, ctx.Err()
	}

	running, degraded := c.startBackground(nx, ny, now)
	mu.Lock()
	defer mu.Unlock()
	res.Degraded = res.Degraded || degraded
	switch {
	case timedOut:
		res.RetryAfter = retryAfterWait
	case running:
		res.RetryAfter = retryAfterBg
	default:
		res.Complete = true
	}
	return res, nil
}

// shared는 키 하나를 즉시 조회로 받는다. 이미 진행 중이면(정기 수집 포함) 그 결과를 기다려 나눈다.
// 최근 실패한 키는 부르지 않는다(f=nil, called=true). 예산에 막히면 called=false.
func (c *Collector) shared(k key, stage budget.Stage) (*flight, bool) {
	now := c.opt.Now()
	c.mu.Lock()
	if f := c.inflight[k]; f != nil {
		c.mu.Unlock()
		<-f.done
		return f, f.called
	}
	c.od.mu.Lock()
	failedAt, failed := c.od.failed[k]
	if failed && now.Sub(failedAt) >= FailTTL {
		delete(c.od.failed, k)
		failed = false
	}
	c.od.mu.Unlock()
	if failed {
		c.mu.Unlock()
		return nil, true
	}
	f := &flight{done: make(chan struct{})}
	c.inflight[k] = f
	c.mu.Unlock()
	defer c.land(k, f)

	res, outcome, errText, _, called := c.call(c.lifeCtx(), task{k, TrigOnDemand}, stage)
	f.called, f.code = called, res.Code
	f.ok = called && res.OK() && outcome != store.OutcomeFailed
	if called && !f.ok {
		c.od.mu.Lock()
		c.od.failed[k] = c.opt.Now()
		c.od.mu.Unlock()
		c.log.Warn("collector: ondemand fetch failed", "op", string(k.op), "nx", k.nx, "ny", k.ny, "base", k.base, "code", res.Code, "err", errText)
	}
	return f, called
}

// startBackground는 이번 시에 아직 시작하지 않았으면 2·3단계를 뒤에서 시작한다.
// running: 지금 돌고 있음. degraded: 예산 단계 때문에 건너뛴 단계가 있음.
func (c *Collector) startBackground(nx, ny int, now time.Time) (running, degraded bool) {
	gk := gridKey{nx, ny}
	hour := kst.ToInt(now.Truncate(time.Hour))
	tight := !c.budget.Allows(string(kma.OpVilage), budget.OnDemand3)
	deg := !c.budget.Allows(string(kma.OpNcst), budget.OnDemand2)
	c.od.mu.Lock()
	st := c.od.bg[gk]
	if st == nil {
		st = &bgState{}
		c.od.bg[gk] = st
	}
	if st.running || st.hour == hour {
		r := st.running
		c.od.mu.Unlock()
		return r, tight || deg
	}
	st.hour, st.running = hour, true
	c.od.gcLocked(now)
	c.od.wg.Add(1)
	c.od.mu.Unlock()

	go func() {
		defer c.od.wg.Done()
		defer func() {
			c.od.mu.Lock()
			st.running = false
			c.od.mu.Unlock()
		}()
		ctx := c.lifeCtx()
		var keys []struct {
			k     key
			stage budget.Stage
		}
		// 2단계: 실황 백필 (최근 시각부터)
		cur := kst.Ncst.Latest(now)
		if n := c.opt.OnDemandBackfill; n > 0 {
			for _, t := range c.backfillTasks(ctx, config.Grid{NX: nx, NY: ny}, cur, n) {
				keys = append(keys, struct {
					k     key
					stage budget.Stage
				}{t.key, budget.OnDemand2})
			}
		}
		// 3단계: 과거 칸 추정용 단기 발표분 (Q2 b)
		latest := kst.Vilage.Latest(now)
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, kst.Loc)
		n := 0
		for _, b := range []time.Time{today.Add(-13 * time.Hour), today.Add(-time.Hour), today.Add(11 * time.Hour)} {
			if n >= c.opt.OnDemandPast || !b.Before(latest) {
				continue
			}
			k := key{kma.OpVilage, nx, ny, kst.ToInt(b)}
			if has, err := c.has(ctx, k); err != nil || has {
				continue
			}
			n++
			keys = append(keys, struct {
				k     key
				stage budget.Stage
			}{k, budget.OnDemand3})
		}
		var wg sync.WaitGroup
		for _, it := range keys {
			wg.Add(1)
			go func(k key, s budget.Stage) {
				defer wg.Done()
				c.shared(k, s)
			}(it.k, it.stage)
		}
		wg.Wait()
	}()
	return true, tight || deg
}

// gcLocked는 오래된 실패 기록과 격자 상태를 지운다(od.mu를 잡은 채로 부른다).
func (o *odState) gcLocked(now time.Time) {
	for k, t := range o.failed {
		if now.Sub(t) >= FailTTL {
			delete(o.failed, k)
		}
	}
	old := kst.ToInt(now.Add(-2 * time.Hour))
	for g, st := range o.bg {
		if !st.running && st.hour < old {
			delete(o.bg, g)
		}
	}
	for g, t := range o.touched {
		if now.Sub(t) >= 24*time.Hour {
			delete(o.touched, g)
		}
	}
}

// Grids는 정기 수집 대상 격자 목록이다(운영 상태용).
func (c *Collector) Grids() [][2]int {
	out := make([][2]int, len(c.opt.Grids))
	for i, g := range c.opt.Grids {
		out[i] = [2]int{g.NX, g.NY}
	}
	return out
}
