package collector

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"myweather/internal/budget"
	"myweather/internal/config"
	"myweather/internal/kma"
	"myweather/internal/kma/kmatest"
	"myweather/internal/kst"
	"myweather/internal/store"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

var (
	gA = config.Grid{NX: 61, NY: 125}
	gB = config.Grid{NX: 59, NY: 126}
)

type env struct {
	t   *testing.T
	srv *kmatest.Server
	db  *store.DB
	now time.Time
	bud *budget.Budget
	api *kma.Client
}

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, kst.Loc)
	if err != nil {
		panic(err)
	}
	return t
}

func newEnv(t *testing.T, limit int) *env {
	t.Helper()
	e := &env{t: t}
	e.srv = kmatest.NewServer(nil)
	t.Cleanup(e.srv.Close)
	db, err := store.Open(filepath.Join(t.TempDir(), "c.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.db = db
	e.bud = budget.New(limit, func() time.Time { return e.now }, quiet)
	e.api = kma.New(e.srv.URL, kmatest.Key, kma.Options{PerSecond: 10000, Timeout: 2 * time.Second})
	return e
}

func (e *env) collector(grids []config.Grid, backfill int) *Collector {
	return New(e.db, e.api, e.bud, Options{
		Grids: grids, StartupBackfill: backfill, Log: quiet,
		Now: func() time.Time { return e.now },
	})
}

type callRow struct {
	op      string
	nx      int
	base    int64
	trigger string
	code    string
	outcome string
}

func (e *env) calls() []callRow {
	rows, err := e.db.R.Query(`SELECT op, nx, base, trigger, result_code, outcome FROM kma_call ORDER BY id`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []callRow
	for rows.Next() {
		var r callRow
		rows.Scan(&r.op, &r.nx, &r.base, &r.trigger, &r.code, &r.outcome)
		out = append(out, r)
	}
	return out
}

func (e *env) callsOf(op kma.Op) []callRow {
	var out []callRow
	for _, c := range e.calls() {
		if c.op == string(op) {
			out = append(out, c)
		}
	}
	return out
}

func (e *env) baseCalls(op kma.Op, base int64) []callRow {
	var out []callRow
	for _, c := range e.callsOf(op) {
		if c.base == base {
			out = append(out, c)
		}
	}
	return out
}

func failFor(op kma.Op, nx int, status int, body string) kmatest.Responder {
	return func(r kmatest.Req) (int, string) {
		if r.Op == string(op) && r.NX == nx {
			return status, body
		}
		return kmatest.OK(r)
	}
}

func TestStartupCatchUpAndDedupe(t *testing.T) {
	e := newEnv(t, 10000)
	e.now = at("2026-11-09 23:20")
	c := e.collector([]config.Grid{gA, gB}, 2)
	// 격자당: 실황 23:00 + 백필 22:00·21:00 + 초단기 22:30 + 단기 23:00 = 5
	if n := c.Step(context.Background()); n != 10 {
		t.Fatalf("calls=%d", n)
	}
	got := map[string]int{}
	for _, r := range e.calls() {
		got[r.op+"/"+r.trigger]++
		if r.code != "00" || r.outcome != store.OutcomeNew {
			t.Fatalf("bad call %+v", r)
		}
	}
	want := map[string]int{
		"getUltraSrtNcst/startup": 2, "getUltraSrtNcst/backfill": 4,
		"getUltraSrtFcst/startup": 2, "getVilageFcst/startup": 2,
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s=%d want %d (all %v)", k, got[k], v, got)
		}
	}
	bases := map[string][]int64{}
	for _, r := range e.calls() {
		if r.nx == 61 {
			bases[r.op] = append(bases[r.op], r.base)
		}
	}
	sort.Slice(bases["getUltraSrtNcst"], func(i, j int) bool { return bases["getUltraSrtNcst"][i] < bases["getUltraSrtNcst"][j] })
	if b := bases["getUltraSrtNcst"]; len(b) != 3 || b[0] != 202611092100 || b[2] != 202611092300 {
		t.Fatalf("ncst bases %v", b)
	}
	if b := bases["getUltraSrtFcst"]; len(b) != 1 || b[0] != 202611092230 {
		t.Fatalf("ultra bases %v", b)
	}
	if b := bases["getVilageFcst"]; len(b) != 1 || b[0] != 202611092300 {
		t.Fatalf("vilage bases %v", b)
	}
	// 같은 시각에 다시 돌면 호출 없음, 몇 분 뒤에도 없음
	if n := c.Step(context.Background()); n != 0 {
		t.Fatalf("second step calls=%d", n)
	}
	e.now = at("2026-11-09 23:44")
	if n := c.Step(context.Background()); n != 0 {
		t.Fatalf("23:44 calls=%d", n)
	}
	// 23:45: 초단기 23:30 발표분 (스케줄)
	e.now = at("2026-11-09 23:45")
	if n := c.Step(context.Background()); n != 2 {
		t.Fatalf("23:45 calls=%d", n)
	}
	last := e.calls()[len(e.calls())-1]
	if last.op != "getUltraSrtFcst" || last.base != 202611092330 || last.trigger != TrigSchedule {
		t.Fatalf("%+v", last)
	}
	// 재시작해도 이미 받은 것은 다시 받지 않는다(백필 상한 23이어도 빠진 시각만)
	c2 := e.collector([]config.Grid{gA}, 0)
	if n := c2.Step(context.Background()); n != 0 {
		t.Fatalf("restart calls=%d", n)
	}
}

func TestNoDataRetryBackoffPerGrid(t *testing.T) {
	e := newEnv(t, 10000)
	e.srv.SetResponder(failFor(kma.OpVilage, 61, 200, kmatest.NoData))
	e.now = at("2026-11-09 23:10")
	c := e.collector([]config.Grid{gA, gB}, 0)
	c.Step(context.Background())
	v := e.callsOf(kma.OpVilage)
	if len(v) != 2 {
		t.Fatalf("%+v", v)
	}
	// 서교동은 저장, 역삼1동은 실패
	if ok, _ := e.db.HasFcst(context.Background(), "vilage", 59, 126, 202611092300); !ok {
		t.Fatal("grid B should be stored")
	}
	if ok, _ := e.db.HasFcst(context.Background(), "vilage", 61, 125, 202611092300); ok {
		t.Fatal("grid A must not be stored")
	}
	steps := []struct {
		now    string
		calls  int
		code   string
		retryN int
	}{
		{"2026-11-09 23:10", 0, "", 0}, // 30초 뒤 같은 분: 아직
		{"2026-11-09 23:11", 1, "03", 1},
		{"2026-11-09 23:12", 0, "", 0},
		{"2026-11-09 23:13", 1, "03", 2}, // +2분
		{"2026-11-09 23:16", 0, "", 0},
		{"2026-11-09 23:17", 1, "03", 3}, // +4분
		{"2026-11-09 23:25", 1, "03", 4}, // +8분
		{"2026-11-09 23:39", 0, "", 0},
		{"2026-11-09 23:40", 1, "03", 5}, // +15분
		{"2026-11-09 23:55", 1, "03", 6}, // +15분 고정
	}
	for _, s := range steps {
		e.now = at(s.now)
		if s.now == "2026-11-09 23:10" {
			e.now = e.now.Add(30 * time.Second)
		}
		before := len(e.baseCalls(kma.OpVilage, 202611092300))
		c.Step(context.Background())
		v := e.baseCalls(kma.OpVilage, 202611092300)
		if len(v)-before != s.calls {
			t.Fatalf("%s: vilage calls %d want %d", s.now, len(v)-before, s.calls)
		}
		if s.calls > 0 {
			last := v[len(v)-1]
			if last.nx != 61 || last.trigger != TrigRetry || last.code != s.code || last.outcome != store.OutcomeFailed {
				t.Fatalf("%s: %+v", s.now, last)
			}
		}
	}
	// 풀리면 다음 재시도에서 저장
	e.srv.SetResponder(nil)
	e.now = at("2026-11-10 00:10")
	c.Step(context.Background())
	if ok, _ := e.db.HasFcst(context.Background(), "vilage", 61, 125, 202611092300); !ok {
		t.Fatal("grid A should be stored after recovery")
	}
}

func TestRetryStopsAtNextBase(t *testing.T) {
	e := newEnv(t, 10000)
	e.srv.SetResponder(func(r kmatest.Req) (int, string) {
		if r.Op == "getVilageFcst" && r.Base == 202611092000 {
			return 200, kmatest.NoData
		}
		return kmatest.OK(r)
	})
	e.now = at("2026-11-09 20:10")
	c := e.collector([]config.Grid{gA}, 0)
	for e.now.Before(at("2026-11-09 23:30")) {
		c.Step(context.Background())
		e.now = e.now.Add(time.Minute)
	}
	var b20, b23 int
	for _, r := range e.callsOf(kma.OpVilage) {
		switch r.base {
		case 202611092000:
			b20++
		case 202611092300:
			b23++
		}
	}
	// 20:10~23:09 사이 20시 발표분: 1+1+1+1+1 (20:10,11,13,17,25) + 15분 간격 (20:40 … 23:10 직전)
	if b20 < 10 || b20 > 16 {
		t.Fatalf("20시 발표분 호출 %d", b20)
	}
	if b23 == 0 {
		t.Fatal("23시 발표분 조회가 시작돼야 함")
	}
	for _, r := range e.callsOf(kma.OpVilage) {
		if r.base == 202611092000 && r.trigger != TrigStartup && r.trigger != TrigRetry {
			t.Fatalf("%+v", r)
		}
	}
	// 23:10 이후에는 20시 발표분을 부르지 않는다
	rows, _ := e.db.R.Query(`SELECT count(*) FROM kma_call WHERE op='getVilageFcst' AND base=202611092000 AND called_at >= ?`, at("2026-11-09 23:10").Unix())
	var n int
	rows.Next()
	rows.Scan(&n)
	rows.Close()
	if n != 0 {
		t.Fatalf("old base called %d times after next base", n)
	}
}

func TestPartialNotStoredAndRetried(t *testing.T) {
	e := newEnv(t, 10000)
	partial := true
	e.srv.SetResponder(func(r kmatest.Req) (int, string) {
		if r.Op == "getUltraSrtFcst" && partial {
			items := kmatest.Items(r, 0)
			return 200, kmatest.Envelope(items[:10], len(items))
		}
		return kmatest.OK(r)
	})
	e.now = at("2026-11-09 23:45")
	c := e.collector([]config.Grid{gA}, 0)
	c.Step(context.Background())
	u := e.callsOf(kma.OpUltra)
	if len(u) != 1 || u[0].code != kma.CodePartial || u[0].outcome != store.OutcomeFailed {
		t.Fatalf("%+v", u)
	}
	if ok, _ := e.db.HasFcst(context.Background(), "ultra", 61, 125, 202611092330); ok {
		t.Fatal("partial must not be stored")
	}
	partial = false
	e.now = at("2026-11-09 23:46")
	c.Step(context.Background())
	if ok, _ := e.db.HasFcst(context.Background(), "ultra", 61, 125, 202611092330); !ok {
		t.Fatal("should be stored on retry")
	}
	var nTotal int
	e.db.R.QueryRow(`SELECT total_count FROM kma_call WHERE op='getUltraSrtFcst' ORDER BY id LIMIT 1`).Scan(&nTotal)
	if nTotal != 60 {
		t.Fatalf("total_count logged=%d", nTotal)
	}
}

func TestAuthErrorNotRetried(t *testing.T) {
	e := newEnv(t, 10000)
	e.srv.SetResponder(func(r kmatest.Req) (int, string) { return 403, kmatest.AuthError403 })
	e.now = at("2026-11-09 23:45")
	c := e.collector([]config.Grid{gA}, 0)
	c.Step(context.Background())
	n1 := len(e.calls())
	if n1 != 3 {
		t.Fatalf("calls=%d", n1)
	}
	for i := 0; i < 10; i++ {
		e.now = e.now.Add(time.Minute)
		c.Step(context.Background())
	}
	// 23:55까지: 재시도 없음 (00:10 실황 발표 전)
	if n := len(e.calls()); n != n1 {
		t.Fatalf("auth retried: %d calls", n)
	}
	for _, r := range e.calls() {
		if r.code != kma.CodeAuth {
			t.Fatalf("%+v", r)
		}
	}
	w := c.Waiting()
	if len(w) != 3 || !w[0].GiveUp {
		t.Fatalf("%+v", w)
	}
}

func refetchSetup(t *testing.T) (*env, *Collector) {
	e := newEnv(t, 10000)
	e.now = at("2026-11-09 23:10")
	c := e.collector([]config.Grid{gA}, 0)
	c.Step(context.Background())
	if v := e.callsOf(kma.OpVilage); len(v) != 1 || v[0].outcome != store.OutcomeNew {
		t.Fatalf("%+v", v)
	}
	return e, c
}

func vilageRefetches(e *env) []callRow {
	var out []callRow
	for _, r := range e.callsOf(kma.OpVilage) {
		if r.trigger == TrigRefetch {
			out = append(out, r)
		}
	}
	return out
}

func TestRefetchSameContent(t *testing.T) {
	e, c := refetchSetup(t)
	e.now = at("2026-11-10 00:09")
	c.Step(context.Background())
	if r := vilageRefetches(e); len(r) != 0 {
		t.Fatalf("too early: %+v", r)
	}
	if w := c.NextWake(at("2026-11-10 00:09").Add(10 * time.Second)); !w.Equal(at("2026-11-10 00:10")) {
		t.Fatalf("next wake %s", w)
	}
	e.now = at("2026-11-10 00:10")
	c.Step(context.Background())
	r := vilageRefetches(e)
	if len(r) != 1 || r[0].base != 202611092300 || r[0].outcome != store.OutcomeSame {
		t.Fatalf("%+v", r)
	}
	vs, _ := e.db.FcstVersions(context.Background(), "vilage", 61, 125, 202611092300)
	if len(vs) != 1 || vs[0].CheckedAt != at("2026-11-10 00:10").Unix() {
		t.Fatalf("%+v", vs)
	}
	// 한 번만
	for _, s := range []string{"2026-11-10 00:30", "2026-11-10 01:15", "2026-11-10 02:00"} {
		e.now = at(s)
		c.Step(context.Background())
	}
	if r := vilageRefetches(e); len(r) != 1 {
		t.Fatalf("refetched again: %+v", r)
	}
}

func TestRefetchChangedContentNewVersion(t *testing.T) {
	e, c := refetchSetup(t)
	e.srv.SetResponder(func(r kmatest.Req) (int, string) { return 200, kmatest.Body(r, 1) })
	e.now = at("2026-11-10 00:10")
	c.Step(context.Background())
	r := vilageRefetches(e)
	if len(r) != 1 || r[0].outcome != store.OutcomeNew {
		t.Fatalf("%+v", r)
	}
	vs, _ := e.db.FcstVersions(context.Background(), "vilage", 61, 125, 202611092300)
	if len(vs) != 2 || vs[1].Ver != 2 {
		t.Fatalf("%+v", vs)
	}
	e.now = at("2026-11-10 01:30")
	c.Step(context.Background())
	if r := vilageRefetches(e); len(r) != 1 {
		t.Fatalf("ver2 should not trigger another refetch: %+v", r)
	}
}

func TestRefetchFailureNotRetried(t *testing.T) {
	e, c := refetchSetup(t)
	e.srv.SetResponder(failFor(kma.OpVilage, 61, 500, kmatest.AppError500))
	for _, s := range []string{"2026-11-10 00:10", "2026-11-10 00:11", "2026-11-10 00:20", "2026-11-10 01:00"} {
		e.now = at(s)
		c.Step(context.Background())
	}
	r := vilageRefetches(e)
	if len(r) != 1 || r[0].code != "HTTP500" || r[0].outcome != store.OutcomeFailed {
		t.Fatalf("%+v", r)
	}
}

func TestRefetchSurvivesRestart(t *testing.T) {
	e, _ := refetchSetup(t)
	// 프로세스 재시작 (메모리 상태 없음)
	e.now = at("2026-11-10 00:15")
	c2 := e.collector([]config.Grid{gA}, 0)
	c2.Step(context.Background())
	r := vilageRefetches(e)
	if len(r) != 1 || r[0].base != 202611092300 || r[0].outcome != store.OutcomeSame {
		t.Fatalf("%+v", r)
	}
}

func TestRefetchWhenFirstSuccessLate(t *testing.T) {
	// 첫 성공이 늦어서 다음 발표분이 나온 뒤에 재수집 시각이 와도, 직전 발표분 재수집은 한다.
	e := newEnv(t, 10000)
	e.now = at("2026-11-09 22:30")
	c := e.collector([]config.Grid{gA}, 0)
	c.Step(context.Background()) // 20시 발표분 22:30에 첫 성공
	e.now = at("2026-11-09 23:30")
	c.Step(context.Background())
	var b20 []callRow
	for _, r := range vilageRefetches(e) {
		if r.base == 202611092000 {
			b20 = append(b20, r)
		}
	}
	if len(b20) != 1 {
		t.Fatalf("%+v", e.callsOf(kma.OpVilage))
	}
}

func TestHourlyBackfillLimit(t *testing.T) {
	e := newEnv(t, 10000)
	e.now = at("2026-11-09 12:10")
	c := e.collector([]config.Grid{gA}, 0) // 시작 백필 0
	c.Step(context.Background())
	if n := len(e.callsOf(kma.OpNcst)); n != 1 {
		t.Fatalf("startup ncst=%d", n)
	}
	// 다음 시간: 정시 1 + 빠진 시각 최대 3
	e.now = at("2026-11-09 13:10")
	c.Step(context.Background())
	var bf []int64
	for _, r := range e.callsOf(kma.OpNcst) {
		if r.trigger == TrigBackfill {
			bf = append(bf, r.base)
		}
	}
	sort.Slice(bf, func(i, j int) bool { return bf[i] > bf[j] })
	if len(bf) != 3 || bf[0] != 202611091100 || bf[2] != 202611090900 {
		t.Fatalf("backfill %v", bf)
	}
	// 같은 시간 안에서는 다시 백필하지 않는다
	e.now = at("2026-11-09 13:40")
	before := len(e.callsOf(kma.OpNcst))
	c.Step(context.Background())
	if len(e.callsOf(kma.OpNcst)) != before {
		t.Fatal("backfill repeated within the hour")
	}
}

func TestNcstStoredHourNotRefetched(t *testing.T) {
	e := newEnv(t, 10000)
	e.now = at("2026-11-09 12:10")
	c := e.collector([]config.Grid{gA}, 0)
	c.Step(context.Background())
	// 12시 실황의 일부 요소만 지워도 그 시각은 받은 것으로 본다. 13시는 새 값(variant 2)으로 저장된다.
	e.db.W.Exec(`DELETE FROM obs_value WHERE category='REH'`)
	e.srv.SetResponder(func(r kmatest.Req) (int, string) { return 200, kmatest.Body(r, 2) })
	e.now = at("2026-11-09 13:10")
	c.Step(context.Background())
	vals, _ := e.db.ObsValues(context.Background(), 61, 125, 202611091200)
	if vals["T1H"] != "7.9" || vals["REH"] != "" {
		// 12시 실황은 저장돼 있으므로(REH만 없어도 시각은 있음) 다시 받지 않는다
		t.Fatalf("%v", vals)
	}
	vals, _ = e.db.ObsValues(context.Background(), 61, 125, 202611091300)
	if vals["T1H"] != "9.9" {
		t.Fatalf("%v", vals)
	}
}

func TestBudgetHardStopsCalls(t *testing.T) {
	e := newEnv(t, 100)
	e.now = at("2026-11-09 23:45")
	e.bud.Restore(20261109, map[string]int{"getVilageFcst": 99})
	c := e.collector([]config.Grid{gA}, 0)
	n := c.Step(context.Background())
	if n != 2 || len(e.callsOf(kma.OpVilage)) != 0 {
		t.Fatalf("n=%d vilage=%d", n, len(e.callsOf(kma.OpVilage)))
	}
	// 자정이 지나면 다시 허용
	e.now = at("2026-11-10 00:10")
	c.Step(context.Background())
	if len(e.callsOf(kma.OpVilage)) != 1 {
		t.Fatal("should resume after KST midnight")
	}
}

func TestErrorTextNeverContainsKey(t *testing.T) {
	e := newEnv(t, 10000)
	e.srv.SetResponder(func(r kmatest.Req) (int, string) { return 500, "echo serviceKey=" + r.Key })
	e.now = at("2026-11-09 23:45")
	c := e.collector([]config.Grid{gA}, 0)
	c.Step(context.Background())
	rows, _ := e.db.R.Query(`SELECT err FROM kma_call`)
	defer rows.Close()
	for rows.Next() {
		var s string
		rows.Scan(&s)
		if strings.Contains(s, kmatest.Key) || !strings.Contains(s, "***") {
			t.Fatalf("err=%q", s)
		}
	}
}

func TestFallbackToPreviousBaseOnEmptyDB(t *testing.T) {
	e := newEnv(t, 10000)
	// 23시 발표분만 NO_DATA. DB가 비어 있으므로 직전(20시) 발표분을 한 번 받는다.
	e.srv.SetResponder(func(r kmatest.Req) (int, string) {
		if r.Op == "getVilageFcst" && r.Base == 202611092300 {
			return 200, kmatest.NoData
		}
		return kmatest.OK(r)
	})
	e.now = at("2026-11-09 23:10")
	c := e.collector([]config.Grid{gA}, 0)
	c.Step(context.Background()) // 23시 실패
	if n := len(e.baseCalls(kma.OpVilage, 202611092000)); n != 0 {
		t.Fatalf("fallback before first failure: %d", n)
	}
	e.now = at("2026-11-09 23:11")
	c.Step(context.Background()) // 23시 재시도 + 20시 대체
	fb := e.baseCalls(kma.OpVilage, 202611092000)
	if len(fb) != 1 || fb[0].trigger != TrigFallback || fb[0].outcome != store.OutcomeNew {
		t.Fatalf("%+v", fb)
	}
	for _, s := range []string{"2026-11-09 23:13", "2026-11-09 23:17", "2026-11-09 23:25"} {
		e.now = at(s)
		c.Step(context.Background())
	}
	if n := len(e.baseCalls(kma.OpVilage, 202611092000)); n != 1 {
		t.Fatalf("fallback repeated: %d", n)
	}
	if n := len(e.baseCalls(kma.OpVilage, 202611091700)); n != 0 {
		t.Fatalf("should not go back further once 20시 stored: %d", n)
	}
}

func TestFallbackGoesBackTwoAtMost(t *testing.T) {
	e := newEnv(t, 10000)
	e.srv.SetResponder(failFor(kma.OpVilage, 61, 200, kmatest.NoData))
	e.now = at("2026-11-09 23:10")
	c := e.collector([]config.Grid{gA}, 0)
	for _, s := range []string{"2026-11-09 23:10", "2026-11-09 23:11", "2026-11-09 23:13", "2026-11-09 23:17", "2026-11-09 23:25"} {
		e.now = at(s)
		c.Step(context.Background())
	}
	if a, b, z := len(e.baseCalls(kma.OpVilage, 202611092000)), len(e.baseCalls(kma.OpVilage, 202611091700)), len(e.baseCalls(kma.OpVilage, 202611091400)); a != 1 || b != 1 || z != 0 {
		t.Fatalf("20시=%d 17시=%d 14시=%d", a, b, z)
	}
}

func TestNoFallbackWhenPreviousStored(t *testing.T) {
	e := newEnv(t, 10000)
	e.now = at("2026-11-09 20:10")
	c := e.collector([]config.Grid{gA}, 0)
	c.Step(context.Background()) // 20시 저장
	e.srv.SetResponder(failFor(kma.OpVilage, 61, 200, kmatest.NoData))
	for _, s := range []string{"2026-11-09 23:10", "2026-11-09 23:11", "2026-11-09 23:13"} {
		e.now = at(s)
		c.Step(context.Background())
	}
	for _, r := range e.callsOf(kma.OpVilage) {
		if r.trigger == TrigFallback {
			t.Fatalf("unexpected fallback %+v", r)
		}
	}
}
