package alert

import (
	"context"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"myweather/internal/kst"
	"myweather/internal/push"
	"myweather/internal/store"
)

type fakePush struct {
	mu   sync.Mutex
	msgs []push.Message
}

func (f *fakePush) SendUser(ctx context.Context, m push.Message, userID int64, endpoint string) (push.Report, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, m)
	return push.Report{Sent: 1}, nil
}

const nx, ny = 59, 126

type tenv struct {
	t   *testing.T
	db  *store.DB
	e   *Engine
	p   *fakePush
	now time.Time
	fav store.Favorite
}

func at(s string) time.Time {
	t, err := time.ParseInLocation("200601021504", s, kst.Loc)
	if err != nil {
		panic(err)
	}
	return t
}

func newEnv(t *testing.T, now string) *tenv {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "a.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if _, _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	te := &tenv{t: t, db: db, p: &fakePush{}, now: at(now)}
	te.e = New(Options{DB: db, Push: te.p, Now: func() time.Time { return te.now }})
	u, err := db.CreateUser(ctx, store.RoleAdmin, te.now)
	if err != nil {
		t.Fatal(err)
	}
	f, _, _, err := db.AddFavorite(ctx, u.ID, store.FavInput{Name: "서교동", Region: "1144066000", NX: nx, NY: ny}, te.now)
	if err != nil {
		t.Fatal(err)
	}
	tr := true
	if err := db.PatchAlerts(ctx, u.ID, []store.AlertPatch{{ID: f.ID, Soon: &tr, Eve: &tr}}); err != nil {
		t.Fatal(err)
	}
	te.fav = f
	return te
}

// fcst는 base 발표분을 저장한다. vals: 시각(YYYYMMDDHHMM) → category → value
func (te *tenv) fcst(op, base string, vals map[string]map[string]string) {
	var rows []store.Row
	for ts, m := range vals {
		v, _ := strconv.ParseInt(ts, 10, 64)
		for c, x := range m {
			rows = append(rows, store.Row{At: v, Category: c, Value: x})
		}
	}
	b, _ := strconv.ParseInt(base, 10, 64)
	if _, _, err := te.db.SaveFcst(context.Background(), op, nx, ny, b, rows, te.now); err != nil {
		te.t.Fatal(err)
	}
}

func (te *tenv) obs(ts string, pty, rn1 string) {
	v, _ := strconv.ParseInt(ts, 10, 64)
	if _, _, err := te.db.SaveObs(context.Background(), nx, ny, v, []store.Row{{At: v, Category: "PTY", Value: pty}, {At: v, Category: "RN1", Value: rn1}, {At: v, Category: "T1H", Value: "10"}}, te.now); err != nil {
		te.t.Fatal(err)
	}
}

// 13:30 발표: 14시(1번째)·15·16·17시(2~4번째)
func (te *tenv) ultra(base string, pty map[string]string) {
	vals := map[string]map[string]string{}
	b := at(base).Truncate(time.Hour)
	for i := 1; i <= 6; i++ {
		ts := b.Add(time.Duration(i) * time.Hour).Format("200601021504")
		p := pty[ts]
		if p == "" {
			p = "0"
		}
		vals[ts] = map[string]string{"PTY": p, "T1H": "10", "RN1": "강수없음"}
	}
	te.fcst("ultra", base, vals)
}

func (te *tenv) vilage(base string, from string, hours int, pop int, pty map[string]string) {
	vals := map[string]map[string]string{}
	f := at(from)
	for i := 0; i < hours; i++ {
		ts := f.Add(time.Duration(i) * time.Hour).Format("200601021504")
		p := pty[ts]
		if p == "" {
			p = "0"
		}
		vals[ts] = map[string]string{"PTY": p, "POP": strconv.Itoa(pop), "TMP": "10", "SKY": "4"}
	}
	te.fcst("vilage", base, vals)
}

func TestSoonE30(t *testing.T) {
	ctx := context.Background()
	te := newEnv(t, "202611101345")
	te.vilage("202611101100", "202611101200", 12, 30, nil)
	for _, h := range []string{"1100", "1200", "1300"} {
		te.obs("20261110"+h, "0", "0")
	}
	// 첫 정시(14시)만 비 → 보내지 않음
	te.ultra("202611101330", map[string]string{"202611101400": "1"})
	if err := te.e.CheckSoon(ctx, nx, ny, 202611101330); err != nil {
		t.Fatal(err)
	}
	if len(te.p.msgs) != 0 {
		t.Fatalf("1st hour should not alert: %+v", te.p.msgs)
	}
	// 3번째 정시(16시) 비, 단기 POP 30 → 보냄
	te.now = at("202611101445")
	te.ultra("202611101430", map[string]string{"202611101700": "1", "202611101800": "1"})
	if err := te.e.CheckSoon(ctx, nx, ny, 202611101430); err != nil {
		t.Fatal(err)
	}
	if len(te.p.msgs) != 1 {
		t.Fatalf("want 1 alert, got %+v", te.p.msgs)
	}
	m := te.p.msgs[0]
	if m.Title != "서교동" || m.Body != "1~3시간 안에 비 소식 (17시 전후)" || m.Tag != "soon-1" || m.URL != "/?fav=1" || m.TTL != time.Hour {
		t.Fatalf("msg %+v", m)
	}
	evs, _ := te.db.AlertEvents(ctx, 10)
	if len(evs) != 1 || evs[0].RainFrom != 202611101700 || evs[0].RainTo != 202611101800 || evs[0].SentN != 1 || evs[0].Source != "ultra:202611101430" {
		t.Fatalf("events %+v", evs)
	}
	// 1시간 뒤에도 비 예보 → 쿨다운(6h)이라 보내지 않음
	te.now = at("202611101545")
	te.ultra("202611101530", map[string]string{"202611101700": "1", "202611101800": "1"})
	te.e.CheckSoon(ctx, nx, ny, 202611101530)
	if len(te.p.msgs) != 1 {
		t.Fatalf("cooldown broken: %d", len(te.p.msgs))
	}
	// 에피소드는 열려 있고 last_rain_seen이 갱신됨
	eps, _ := te.db.OpenEpisodes(ctx, te.fav.ID)
	if len(eps) != 1 || eps[0].LastRainSeen != te.now.Unix() {
		t.Fatalf("episodes %+v", eps)
	}
	// 비 없음이 3시간 넘으면 닫힘
	te.now = at("202611101945")
	te.ultra("202611101930", nil)
	te.obs("202611101900", "0", "0")
	te.e.CheckSoon(ctx, nx, ny, 202611101930)
	if eps, _ := te.db.OpenEpisodes(ctx, te.fav.ID); len(eps) != 0 {
		t.Fatalf("episode not closed: %+v", eps)
	}
}

func TestSoonGates(t *testing.T) {
	ctx := context.Background()
	// 단기 POP 20·PTY 0 → 게이트에 걸림
	te := newEnv(t, "202611101445")
	te.vilage("202611101400", "202611101500", 12, 20, nil)
	te.ultra("202611101430", map[string]string{"202611101600": "1"})
	te.e.CheckSoon(ctx, nx, ny, 202611101430)
	if len(te.p.msgs) != 0 {
		t.Fatal("POP gate ignored")
	}
	// 단기 POP 20이지만 ±1시간에 단기 PTY → 통과
	te.vilage("202611101400", "202611101500", 12, 20, map[string]string{"202611101700": "1"})
	te.ultra("202611101430", map[string]string{"202611101600": "3"})
	te.e.CheckSoon(ctx, nx, ny, 202611101430)
	if len(te.p.msgs) != 1 || te.p.msgs[0].Body != "1~3시간 안에 눈 소식 (16시 전후)" {
		t.Fatalf("vilage PTY gate: %+v", te.p.msgs)
	}

	// 최근 3시간 실황에 비 → 보내지 않음. 빗방울(PTY 5) 예보도 보내지 않음.
	te2 := newEnv(t, "202611101445")
	te2.vilage("202611101400", "202611101500", 12, 60, nil)
	te2.obs("202611101200", "1", "0.5")
	te2.ultra("202611101430", map[string]string{"202611101600": "1"})
	te2.e.CheckSoon(ctx, nx, ny, 202611101430)
	te3 := newEnv(t, "202611101445")
	te3.vilage("202611101400", "202611101500", 12, 60, nil)
	te3.ultra("202611101430", map[string]string{"202611101600": "5"})
	te3.e.CheckSoon(ctx, nx, ny, 202611101430)
	if len(te2.p.msgs)+len(te3.p.msgs) != 0 {
		t.Fatal("obs rain / drizzle should not alert")
	}

	// 알림이 꺼진 즐겨찾기는 판정하지 않음
	te4 := newEnv(t, "202611101445")
	f := false
	te4.db.PatchAlerts(ctx, te4.fav.UserID, []store.AlertPatch{{ID: te4.fav.ID, Soon: &f}})
	te4.vilage("202611101400", "202611101500", 12, 60, nil)
	te4.ultra("202611101430", map[string]string{"202611101600": "1"})
	te4.e.CheckSoon(ctx, nx, ny, 202611101430)
	if len(te4.p.msgs) != 0 {
		t.Fatal("disabled favorite alerted")
	}
}

func TestEve(t *testing.T) {
	ctx := context.Background()
	te := newEnv(t, "202611102200")
	// 17시 발표분만 있음: 내일 15~20시 비
	pty := map[string]string{}
	for h := 15; h <= 20; h++ {
		pty["20261111"+strconv.Itoa(h)+"00"] = "1"
	}
	te.vilage("202611101700", "202611110000", 24, 40, pty)
	if err := te.e.CheckEve(ctx, te.now); err != nil {
		t.Fatal(err)
	}
	if len(te.p.msgs) != 1 || te.p.msgs[0].Body != "내일 15~21시 비, 최대 40%" || te.p.msgs[0].Tag != "eve-1" || te.p.msgs[0].TTL != 8*time.Hour {
		t.Fatalf("eve %+v", te.p.msgs)
	}
	// 다시 불러도 한 번만
	te.now = at("202611102310")
	te.e.CheckEve(ctx, te.now)
	if len(te.p.msgs) != 1 {
		t.Fatal("eve repeated")
	}

	// 20시 발표분이 있으면 그것을 씀: POP 60 한 시각, 눈
	te2 := newEnv(t, "202611102200")
	te2.vilage("202611101700", "202611110000", 24, 70, nil)
	pop := map[string]string{"202611110900": "3"}
	te2.vilage("202611102000", "202611110000", 24, 10, pop)
	te2.e.CheckEve(ctx, te2.now)
	if len(te2.p.msgs) != 1 || te2.p.msgs[0].Body != "내일 9시 눈, 최대 10%" {
		t.Fatalf("eve 20h base: %+v", te2.p.msgs)
	}
	// 비 없음 → 보내지 않음
	te3 := newEnv(t, "202611102200")
	te3.vilage("202611102000", "202611110000", 24, 20, nil)
	te3.e.CheckEve(ctx, te3.now)
	if len(te3.p.msgs) != 0 {
		t.Fatal("no rain but alerted")
	}
}

func TestNoPushRecordsOnly(t *testing.T) {
	ctx := context.Background()
	te := newEnv(t, "202611101445")
	te.e = New(Options{DB: te.db, Now: func() time.Time { return te.now }})
	te.vilage("202611101400", "202611101500", 12, 60, nil)
	te.ultra("202611101430", map[string]string{"202611101600": "1"})
	if err := te.e.CheckSoon(ctx, nx, ny, 202611101430); err != nil {
		t.Fatal(err)
	}
	evs, _ := te.db.AlertEvents(ctx, 10)
	if len(evs) != 1 || evs[0].SentAt != 0 || evs[0].SentN != 0 {
		t.Fatalf("events %+v", evs)
	}
}

func TestRunQueue(t *testing.T) {
	te := newEnv(t, "202611101445")
	te.vilage("202611101400", "202611101500", 12, 60, nil)
	te.ultra("202611101430", map[string]string{"202611101600": "1"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { te.e.Run(ctx); close(done) }()
	te.e.OnUltra(nx, ny, 202611101430)
	deadline := time.Now().Add(3 * time.Second)
	for {
		te.p.mu.Lock()
		n := len(te.p.msgs)
		te.p.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queue not processed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}
