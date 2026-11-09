// Package collector는 설정 격자의 실황·초단기예보·단기예보를 발표 시각에 맞춰 수집한다(설계 §5).
//
//   - 발표 시각 트리거: 실황 hh:10, 초단기 hh:45, 단기 발표 +10분.
//   - 시작할 때 따라잡기: 지금 받을 수 있는 최신 발표분이 DB에 없으면 바로 받고, 실황은 지난 시각을 백필한다.
//   - 재시도: NO_DATA·PARTIAL·5xx·네트워크·파싱 실패는 1→2→4→8→15분, 이후 15분 간격으로
//     다음 발표분의 첫 조회 시각까지 격자마다 따로 재시도한다. 이전 발표분 대체는 읽기 쪽이 저장된 최신 발표분으로 하고,
//     저장된 직전 발표분이 없을 때만(새 DB 등) 직전 발표분을 최대 2개까지 거슬러 한 번씩 받는다(trigger=fallback).
//   - 단기예보는 첫 성공 1시간 뒤 1회 재수집한다. 재수집 여부는 DB(fcst_version)에서 유도하므로 재시작해도 유지된다.
//   - 저장은 내용 해시로 중복 제거한다(store.SaveFcst / SaveObs).
//
// 즉시 조회(ondemand.go)와 정기 수집은 같은 flight 맵을 써서 같은 키를 한 번만 보낸다.
package collector

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"myweather/internal/budget"
	"myweather/internal/config"
	"myweather/internal/kma"
	"myweather/internal/kst"
	"myweather/internal/store"
)

// Fetcher는 KMA 호출 인터페이스다(*kma.Client가 구현).
type Fetcher interface {
	Fetch(ctx context.Context, op kma.Op, nx, ny int, base time.Time) kma.Result
}

// 트리거 (kma_call.trigger)
const (
	TrigSchedule = "schedule"
	TrigRetry    = "retry"
	TrigRefetch  = "refetch"
	TrigBackfill = "backfill"
	TrigStartup  = "startup"
	TrigFallback = "fallback" // 최신 발표분이 실패하고 저장된 직전 발표분도 없을 때 직전 발표분 조회
)

// RefetchAfter는 단기예보 재수집 지연이다(설계 §5.4).
const RefetchAfter = time.Hour

// HourlyBackfill은 매시 실황 작업에서 채우는 빠진 시각의 최대 개수다(설계 §5.3).
const HourlyBackfill = 3

var retryDelays = []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute}

func retryDelay(tries int) time.Duration {
	if tries < 1 {
		tries = 1
	}
	if tries > len(retryDelays) {
		return retryDelays[len(retryDelays)-1]
	}
	return retryDelays[tries-1]
}

// Options는 수집기 설정이다.
type Options struct {
	Grids           []config.Grid
	StartupBackfill int              // 시작할 때 격자당 실황 백필 최대 시각 수 (0~23)
	Now             func() time.Time // nil이면 kst.Now. 테스트는 가짜 시계를 넣는다
	Log             *slog.Logger

	// 즉시 조회(ondemand.go)
	Context          context.Context // 즉시 조회 호출에 쓸 서버 수명 ctx. nil이면 Background
	OnDemandBackfill int             // 2단계 실황 백필 최대 시각 수 (0이면 건너뜀)
	OnDemandPast     int             // 3단계 과거 단기 발표분 최대 개수 (0이면 건너뜀)
}

type key struct {
	op     kma.Op
	nx, ny int
	base   int64
}

type jobState struct {
	tries    int
	nextAt   time.Time
	lastCode string
	giveUp   bool
}

type task struct {
	key
	trigger string
}

// Collector는 수집 스케줄러다.
type Collector struct {
	db     *store.DB
	api    Fetcher
	budget *budget.Budget
	opt    Options
	log    *slog.Logger

	mu            sync.Mutex
	started       bool
	jobs          map[key]*jobState // 최신 발표분 수집 상태 (재시도)
	refetchTried  map[key]bool      // 이 프로세스에서 재수집을 시도한 발표분 (실패해도 다시 하지 않음)
	fallbackTried map[key]bool      // 이 프로세스에서 대체용으로 조회한 이전 발표분 (실패해도 다시 하지 않음)
	refetchDue    map[key]time.Time // 아직 때가 안 된 재수집 (다음 깨울 시각 계산용)
	backfillHour  map[config.Grid]int64
	inflight      map[key]*flight // 진행 중인 호출. 같은 키는 한 번만 보내고 결과를 나눈다(singleflight)

	od odState // 즉시 조회 상태 (ondemand.go)
}

// New는 수집기를 만든다.
func New(db *store.DB, api Fetcher, b *budget.Budget, opt Options) *Collector {
	if opt.Now == nil {
		opt.Now = kst.Now
	}
	if opt.Log == nil {
		opt.Log = slog.Default()
	}
	if opt.StartupBackfill < 0 {
		opt.StartupBackfill = 0
	}
	if opt.StartupBackfill > 23 {
		opt.StartupBackfill = 23
	}
	return &Collector{
		db: db, api: api, budget: b, opt: opt, log: opt.Log,
		jobs:          map[key]*jobState{},
		refetchTried:  map[key]bool{},
		fallbackTried: map[key]bool{},
		refetchDue:    map[key]time.Time{},
		backfillHour:  map[config.Grid]int64{},
		inflight:      map[key]*flight{},
		od:            newOdState(),
	}
}

// Run은 ctx가 끝날 때까지 수집한다. 다음 작업 시각에 타이머를 건다(최대 1분 간격으로 다시 계산).
func (c *Collector) Run(ctx context.Context) error {
	for {
		c.Step(ctx)
		now := c.opt.Now()
		wake := c.NextWake(now)
		d := wake.Sub(now)
		if d < time.Second {
			d = time.Second
		}
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// Step은 지금 할 일을 모두 실행하고 끝날 때까지 기다린다. 실제 보낸 호출 수를 돌려준다.
func (c *Collector) Step(ctx context.Context) int {
	now := c.opt.Now()
	c.mu.Lock()
	first := !c.started
	c.started = true
	c.mu.Unlock()

	tasks := c.plan(ctx, now, first)
	var wg sync.WaitGroup
	var mu sync.Mutex
	n := 0
	sem := make(chan struct{}, 4)
	for _, t := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(t task) {
			defer wg.Done()
			defer func() { <-sem }()
			if c.exec(ctx, t) {
				mu.Lock()
				n++
				mu.Unlock()
			}
		}(t)
	}
	wg.Wait()
	return n
}

// plan은 now 시점에 실행할 작업 목록을 만든다.
func (c *Collector) plan(ctx context.Context, now time.Time, first bool) []task {
	var tasks []task
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, g := range c.opt.Grids {
		for _, op := range kma.Ops {
			k := op.Kind()
			cur := k.Latest(now)
			ck := key{op, g.NX, g.NY, kst.ToInt(cur)}
			c.gc(op, g, ck.base)

			has, err := c.has(ctx, ck)
			if err != nil {
				c.log.Error("collector: db read failed", "op", op, "grid", g.String(), "err", err)
				continue
			}
			if !has {
				st := c.jobs[ck]
				switch {
				case st == nil:
					trig := TrigSchedule
					if first {
						trig = TrigStartup
					}
					tasks = append(tasks, task{ck, trig})
				case !st.giveUp && !now.Before(st.nextAt):
					tasks = append(tasks, task{ck, TrigRetry})
				}
				if st != nil && st.tries >= 1 && !st.giveUp && op != kma.OpNcst {
					if ft, ok := c.fallbackTask(ctx, op, g, cur); ok {
						tasks = append(tasks, ft)
					}
				}
			} else {
				delete(c.jobs, ck)
			}

			if op == kma.OpVilage {
				for _, b := range []time.Time{cur, k.Prev(cur)} {
					rk := key{op, g.NX, g.NY, kst.ToInt(b)}
					if c.refetchTried[rk] || c.inflight[rk] != nil {
						continue
					}
					due, ok, err := c.refetchDueAt(ctx, rk)
					if err != nil {
						c.log.Error("collector: db read failed", "op", op, "grid", g.String(), "err", err)
						continue
					}
					if !ok {
						delete(c.refetchDue, rk)
						continue
					}
					if now.Before(due) {
						c.refetchDue[rk] = due
						continue
					}
					delete(c.refetchDue, rk)
					tasks = append(tasks, task{rk, TrigRefetch})
				}
			}

			if op == kma.OpNcst && c.backfillHour[g] != ck.base {
				c.backfillHour[g] = ck.base
				limit := HourlyBackfill
				if first {
					limit = c.opt.StartupBackfill
				}
				tasks = append(tasks, c.backfillTasks(ctx, g, cur, limit)...)
			}
		}
	}
	// 같은 키가 겹치지 않게 한다(예: 재수집 대상과 최신 발표분 수집이 같은 base).
	seen := map[key]bool{}
	out := tasks[:0]
	for _, t := range tasks {
		if seen[t.key] || c.inflight[t.key] != nil {
			continue
		}
		seen[t.key] = true
		out = append(out, t)
	}
	return out
}

// gc는 현재 발표분이 아닌 재시도 상태와 오래된 재수집 기록을 지운다(다음 발표분이 나오면 재시도를 멈춘다).
func (c *Collector) gc(op kma.Op, g config.Grid, curBase int64) {
	old := kst.ToInt(kst.FromInt(curBase).Add(-24 * time.Hour))
	for k := range c.jobs {
		if k.op == op && k.nx == g.NX && k.ny == g.NY && k.base != curBase {
			delete(c.jobs, k)
		}
	}
	for k := range c.refetchTried {
		if k.op == op && k.nx == g.NX && k.ny == g.NY && k.base < old {
			delete(c.refetchTried, k)
		}
	}
	for k := range c.fallbackTried {
		if k.op == op && k.nx == g.NX && k.ny == g.NY && k.base < old {
			delete(c.fallbackTried, k)
		}
	}
}

// fallbackTask: 최신 발표분이 한 번 이상 실패했고 직전 발표분 2개가 모두 저장돼 있지 않으면
// 그중 아직 시도하지 않은 가장 최근 것을 한 번 조회한다(설계 §5.3의 '최대 2개까지 거슬러 조회').
// 평소에는 직전 발표분이 이미 저장돼 있어서 호출이 없다. 새 DB에서 첫 발표분이 계속 NO_DATA일 때 쓴다.
func (c *Collector) fallbackTask(ctx context.Context, op kma.Op, g config.Grid, cur time.Time) (task, bool) {
	k := op.Kind()
	p1 := key{op, g.NX, g.NY, kst.ToInt(k.Prev(cur))}
	p2 := key{op, g.NX, g.NY, kst.ToInt(k.Prev(k.Prev(cur)))}
	for _, pk := range []key{p1, p2} {
		has, err := c.has(ctx, pk)
		if err != nil || has {
			return task{}, false
		}
	}
	for _, pk := range []key{p1, p2} {
		if !c.fallbackTried[pk] && c.inflight[pk] == nil {
			return task{pk, TrigFallback}, true
		}
	}
	return task{}, false
}

func (c *Collector) has(ctx context.Context, k key) (bool, error) {
	if k.op == kma.OpNcst {
		m, err := c.db.ObsTimes(ctx, k.nx, k.ny, k.base, k.base)
		return m[k.base], err
	}
	return c.db.HasFcst(ctx, k.op.Short(), k.nx, k.ny, k.base)
}

// refetchDueAt: 버전이 하나뿐이고 한 번도 다시 확인하지 않았으면(checked_at == fetched_at) 재수집 대상이다.
func (c *Collector) refetchDueAt(ctx context.Context, k key) (time.Time, bool, error) {
	vs, err := c.db.FcstVersions(ctx, k.op.Short(), k.nx, k.ny, k.base)
	if err != nil || len(vs) != 1 || vs[0].CheckedAt != vs[0].FetchedAt {
		return time.Time{}, false, err
	}
	return time.Unix(vs[0].FetchedAt, 0).In(kst.Loc).Add(RefetchAfter), true, nil
}

// backfillTasks는 cur 직전 23시간 중 실황이 없는 시각을 최근 것부터 limit개 고른다.
func (c *Collector) backfillTasks(ctx context.Context, g config.Grid, cur time.Time, limit int) []task {
	if limit <= 0 {
		return nil
	}
	from := cur.Add(-23 * time.Hour)
	have, err := c.db.ObsTimes(ctx, g.NX, g.NY, kst.ToInt(from), kst.ToInt(cur))
	if err != nil {
		c.log.Error("collector: db read failed", "grid", g.String(), "err", err)
		return nil
	}
	var out []task
	for t := cur.Add(-time.Hour); !t.Before(from) && len(out) < limit; t = t.Add(-time.Hour) {
		if b := kst.ToInt(t); !have[b] {
			out = append(out, task{key{kma.OpNcst, g.NX, g.NY, b}, TrigBackfill})
		}
	}
	return out
}

// NextWake는 다음에 Step을 부를 시각이다: 다음 발표분 첫 조회, 재시도, 재수집 중 가장 이른 것(최대 1분 뒤).
func (c *Collector) NextWake(now time.Time) time.Time {
	wake := now.Add(time.Minute)
	min := func(t time.Time) {
		if t.After(now) && t.Before(wake) {
			wake = t
		}
	}
	for _, op := range kma.Ops {
		k := op.Kind()
		min(k.FirstFetch(k.Next(k.Latest(now))))
	}
	c.mu.Lock()
	for _, st := range c.jobs {
		if !st.giveUp {
			min(st.nextAt)
		}
	}
	for _, t := range c.refetchDue {
		min(t)
	}
	c.mu.Unlock()
	return wake
}

// exec는 작업 하나를 실행한다. 실제로 KMA를 불렀으면 true.
// 같은 키가 이미 진행 중이면(즉시 조회 포함) 건너뛴다. 다음 Step에서 저장 여부를 다시 본다.
func (c *Collector) exec(ctx context.Context, t task) bool {
	c.mu.Lock()
	if c.inflight[t.key] != nil {
		c.mu.Unlock()
		return false
	}
	f := &flight{done: make(chan struct{})}
	c.inflight[t.key] = f
	c.mu.Unlock()
	defer c.land(t.key, f)

	op := string(t.op)
	logArgs := []any{"op", op, "nx", t.nx, "ny", t.ny, "base", t.base, "trigger", t.trigger}
	if t.trigger == TrigRefetch || t.trigger == TrigFallback {
		c.mu.Lock()
		if t.trigger == TrigRefetch {
			c.refetchTried[t.key] = true // 실패해도 재시도하지 않는다(설계 §5.4)
		} else {
			c.fallbackTried[t.key] = true
		}
		c.mu.Unlock()
	}
	res, outcome, errText, ver, called := c.call(ctx, t, budget.Favorite)
	f.ok, f.called, f.code = outcome != store.OutcomeFailed && called, called, res.Code
	if !called {
		c.log.Warn("collector: daily budget exhausted, skipped", logArgs...)
		c.mu.Lock()
		if t.trigger == TrigSchedule || t.trigger == TrigStartup || t.trigger == TrigRetry {
			st := c.state(t.key)
			st.nextAt = c.opt.Now().Add(15 * time.Minute)
		}
		c.mu.Unlock()
		return false
	}

	ok := outcome != store.OutcomeFailed
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case ok:
		delete(c.jobs, t.key)
		lvl := slog.LevelInfo
		msg := "collector: fetched"
		if t.trigger == TrigRefetch && outcome == store.OutcomeNew {
			msg = "collector: base changed on refetch"
		}
		c.log.Log(ctx, lvl, msg, append(logArgs, "outcome", outcome, "ver", ver, "rows", len(res.Items), "ms", res.Duration.Milliseconds())...)
	case t.trigger == TrigRefetch || t.trigger == TrigBackfill || t.trigger == TrigFallback:
		// 재수집·대체 조회 실패는 재시도하지 않고, 백필 실패는 다음 시간 백필에서 다시 고른다.
		c.log.Warn("collector: fetch failed", append(logArgs, "code", res.Code, "err", errText)...)
	default:
		st := c.state(t.key)
		st.tries++
		st.lastCode = res.Code
		retryable := res.Retryable() || (res.OK() && outcome == store.OutcomeFailed)
		if retryable {
			st.nextAt = c.opt.Now().Add(retryDelay(st.tries))
			c.log.Warn("collector: fetch failed, will retry", append(logArgs, "code", res.Code, "tries", st.tries, "next", st.nextAt.Format("15:04"), "err", errText)...)
		} else {
			st.giveUp = true
			lvl := slog.LevelWarn
			if res.Code == kma.CodeAuth {
				lvl = slog.LevelError
			}
			c.log.Log(ctx, lvl, "collector: fetch failed, not retrying this base", append(logArgs, "code", res.Code, "err", errText)...)
		}
	}
	return true
}

// flight는 진행 중인 호출 하나다. done이 닫히면 결과 필드를 읽을 수 있다.
type flight struct {
	done   chan struct{}
	ok     bool   // 저장까지 성공
	called bool   // 실제로 KMA를 불렀음 (예산에 막히면 false)
	code   string // kma 결과 코드
}

func (c *Collector) land(k key, f *flight) {
	c.mu.Lock()
	delete(c.inflight, k)
	c.mu.Unlock()
	close(f.done)
}

// call은 예산 확인 → KMA 호출 → 저장 → kma_call 기록을 한다. 예산에 막히면 called=false(기록 없음).
func (c *Collector) call(ctx context.Context, t task, stage budget.Stage) (res kma.Result, outcome, errText string, ver int, called bool) {
	op := string(t.op)
	if !c.budget.Reserve(op, stage) {
		return kma.Result{}, store.OutcomeFailed, "", 0, false
	}
	logArgs := []any{"op", op, "nx", t.nx, "ny", t.ny, "base", t.base, "trigger", t.trigger}
	calledAt := c.opt.Now()
	res = c.api.Fetch(ctx, t.op, t.nx, t.ny, kst.FromInt(t.base))
	outcome = store.OutcomeFailed
	errText = res.Err
	if res.OK() {
		var err error
		outcome, ver, err = c.save(ctx, t, res, calledAt)
		if err != nil {
			outcome, errText = store.OutcomeFailed, "store: "+err.Error()
			c.log.Error("collector: save failed", append(logArgs, "err", err)...)
		}
	}
	if err := c.db.LogCall(ctx, store.Call{
		CalledAt: calledAt, Op: op, NX: t.nx, NY: t.ny, Base: t.base, Trigger: t.trigger,
		HTTPStatus: res.HTTPStatus, ResultCode: res.Code, NItems: res.NItems, TotalCount: res.TotalCount,
		Ms: res.Duration.Milliseconds(), Outcome: outcome, Err: errText,
	}); err != nil {
		c.log.Error("collector: kma_call insert failed", append(logArgs, "err", err)...)
	}
	return res, outcome, errText, ver, true
}

func (c *Collector) state(k key) *jobState {
	st := c.jobs[k]
	if st == nil {
		st = &jobState{}
		c.jobs[k] = st
	}
	return st
}

func (c *Collector) save(ctx context.Context, t task, res kma.Result, now time.Time) (string, int, error) {
	rows := make([]store.Row, len(res.Items))
	for i, it := range res.Items {
		rows[i] = store.Row{At: it.At, Category: it.Category, Value: it.Value}
	}
	if t.op == kma.OpNcst {
		out, changes, err := c.db.SaveObs(ctx, t.nx, t.ny, t.base, rows, now)
		for _, ch := range changes {
			c.log.Warn("collector: obs value changed", "nx", t.nx, "ny", t.ny, "obs_at", t.base, "category", ch.Category, "old", ch.Old, "new", ch.New)
		}
		return out, 0, err
	}
	return c.db.SaveFcst(ctx, t.op.Short(), t.nx, t.ny, t.base, rows, now)
}

// Status는 오퍼레이션·격자별 재시도 대기 상태다(운영 로그·다음 단계 /api/health용).
type Status struct {
	Op       string
	NX, NY   int
	Base     int64
	Tries    int
	LastCode string
	GiveUp   bool
}

// Waiting은 아직 받지 못한 최신 발표분 목록이다.
func (c *Collector) Waiting() []Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Status
	for k, st := range c.jobs {
		out = append(out, Status{string(k.op), k.nx, k.ny, k.base, st.tries, st.lastCode, st.giveUp})
	}
	return out
}
