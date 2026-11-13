package store

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"myweather/internal/kst"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "t.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, _, err := d.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestMigrateIdempotentAndPragmas(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	from, to, err := d.Migrate(ctx)
	if err != nil || from != to || to != LatestSchema() || to < 1 {
		t.Fatalf("second migrate: %d→%d %v", from, to, err)
	}
	var mode string
	if err := d.R.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode=%q %v", mode, err)
	}
	// 읽기 풀의 여러 연결 모두에 PRAGMA가 적용됐는지
	for i := 0; i < 3; i++ {
		var fk, bt int
		conn, err := d.R.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk)
		conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&bt)
		defer conn.Close()
		if fk != 1 || bt != 5000 {
			t.Fatalf("conn %d: foreign_keys=%d busy_timeout=%d", i, fk, bt)
		}
	}
	// 0001·0002 테이블만 있어야 한다
	rows, _ := d.R.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	var names []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	rows.Close()
	want := []string{"alert_event", "alert_pref", "app_user", "enroll_token", "favorite", "fcst_value", "fcst_version", "grid_seen",
		"kma_call", "meta", "obs_value", "passkey", "push_sub", "session"}
	if len(names) != len(want) {
		t.Fatalf("tables=%v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("tables=%v", names)
		}
	}
	// 읽기 풀은 쓰기 불가
	if _, err := d.R.ExecContext(ctx, "INSERT INTO grid_seen VALUES (1,1,0,0,NULL)"); err == nil {
		t.Fatal("reader should be query_only")
	}
}

func TestContentHashOrderIndependent(t *testing.T) {
	a := []Row{{1, "TMP", "3"}, {1, "SKY", "1"}, {2, "TMP", "4"}}
	b := []Row{{2, "TMP", "4"}, {1, "SKY", "1"}, {1, "TMP", "3"}}
	if !bytes.Equal(ContentHash(a), ContentHash(b)) || len(ContentHash(a)) != 16 {
		t.Fatal("hash should not depend on order")
	}
	if a[0].Category != "TMP" {
		t.Fatal("input mutated")
	}
	c := []Row{{1, "TMP", "3"}, {1, "SKY", "1"}, {2, "TMP", "5"}}
	if bytes.Equal(ContentHash(a), ContentHash(c)) {
		t.Fatal("different content same hash")
	}
}

func TestSaveFcstDedupe(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	t0 := time.Date(2026, 11, 9, 20, 10, 0, 0, kst.Loc)
	rows := []Row{{202611092100, "TMP", "8"}, {202611092100, "SKY", "1"}}

	out, ver, err := d.SaveFcst(ctx, "vilage", 61, 125, 202611092000, rows, t0)
	if err != nil || out != OutcomeNew || ver != 1 {
		t.Fatalf("first: %s %d %v", out, ver, err)
	}
	// 순서만 다른 같은 내용 → same, checked_at만 갱신
	rev := []Row{rows[1], rows[0]}
	out, ver, err = d.SaveFcst(ctx, "vilage", 61, 125, 202611092000, rev, t0.Add(time.Hour))
	if err != nil || out != OutcomeSame || ver != 1 {
		t.Fatalf("same: %s %d %v", out, ver, err)
	}
	vs, _ := d.FcstVersions(ctx, "vilage", 61, 125, 202611092000)
	if len(vs) != 1 || vs[0].CheckedAt != t0.Add(time.Hour).Unix() || vs[0].FetchedAt != t0.Unix() || vs[0].NRows != 2 {
		t.Fatalf("versions=%+v", vs)
	}
	// 내용 변경 → ver 2
	rows2 := []Row{{202611092100, "TMP", "9"}, {202611092100, "SKY", "1"}}
	out, ver, err = d.SaveFcst(ctx, "vilage", 61, 125, 202611092000, rows2, t0.Add(2*time.Hour))
	if err != nil || out != OutcomeNew || ver != 2 {
		t.Fatalf("changed: %s %d %v", out, ver, err)
	}
	v2, _ := d.FcstValues(ctx, "vilage", 61, 125, 202611092000, 2)
	if len(v2) != 2 || v2[1].Value != "9" {
		t.Fatalf("v2=%+v", v2)
	}
	v1, _ := d.FcstValues(ctx, "vilage", 61, 125, 202611092000, 1)
	if len(v1) != 2 || v1[1].Value != "8" {
		t.Fatalf("v1 should be kept: %+v", v1)
	}
	// 다른 격자는 별개
	if ok, _ := d.HasFcst(ctx, "vilage", 59, 126, 202611092000); ok {
		t.Fatal("other grid")
	}
}

func TestSaveObs(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	t0 := time.Date(2026, 11, 9, 20, 10, 0, 0, kst.Loc)
	rows := []Row{{202611092000, "T1H", "7.9"}, {202611092000, "RN1", "0"}}
	out, ch, err := d.SaveObs(ctx, 61, 125, 202611092000, rows, t0)
	if err != nil || out != OutcomeNew || len(ch) != 0 {
		t.Fatalf("%s %v %v", out, ch, err)
	}
	out, ch, err = d.SaveObs(ctx, 61, 125, 202611092000, rows, t0.Add(time.Minute))
	if err != nil || out != OutcomeSame || len(ch) != 0 {
		t.Fatalf("%s %v %v", out, ch, err)
	}
	rows[0].Value = "8.1"
	out, ch, err = d.SaveObs(ctx, 61, 125, 202611092000, rows, t0.Add(2*time.Minute))
	if err != nil || out != OutcomeNew || len(ch) != 1 || ch[0].Old != "7.9" || ch[0].New != "8.1" {
		t.Fatalf("%s %v %v", out, ch, err)
	}
	vals, _ := d.ObsValues(ctx, 61, 125, 202611092000)
	if vals["T1H"] != "8.1" {
		t.Fatalf("%v", vals)
	}
	ts, _ := d.ObsTimes(ctx, 61, 125, 202611091900, 202611092100)
	if !ts[202611092000] || len(ts) != 1 {
		t.Fatalf("%v", ts)
	}
}

func TestCallLogCounts(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	// 2026-11-09 23:59 KST와 2026-11-10 00:01 KST는 다른 날
	late := time.Date(2026, 11, 9, 23, 59, 0, 0, kst.Loc)
	early := time.Date(2026, 11, 10, 0, 1, 0, 0, kst.Loc)
	for i, c := range []Call{
		{CalledAt: late, Op: "getVilageFcst", Base: 202611092000, Trigger: "schedule", HTTPStatus: 200, ResultCode: "00", Outcome: OutcomeNew},
		{CalledAt: late, Op: "getUltraSrtNcst", Base: 202611092300, Trigger: "retry", HTTPStatus: 200, ResultCode: "03", Outcome: OutcomeFailed, Err: "x"},
		{CalledAt: early.UTC(), Op: "getVilageFcst", Base: 202611092300, Trigger: "schedule", HTTPStatus: 200, ResultCode: "00"},
	} {
		if err := d.LogCall(ctx, c); err != nil {
			t.Fatal(i, err)
		}
	}
	m, err := d.CallCountsByOp(ctx, 20261109)
	if err != nil || m["getVilageFcst"] != 1 || m["getUltraSrtNcst"] != 1 {
		t.Fatalf("%v %v", m, err)
	}
	m, _ = d.CallCountsByOp(ctx, 20261110)
	if m["getVilageFcst"] != 1 || len(m) != 1 {
		t.Fatalf("%v", m)
	}
}

func TestMigrateRefusesNewerSchema(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	if _, err := d.W.ExecContext(ctx, "PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.Migrate(ctx); err == nil {
		t.Fatal("migrate must refuse a DB newer than the binary")
	}
}

func TestBackup(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	now := time.Date(2026, 11, 9, 20, 10, 0, 0, kst.Loc)
	if _, _, err := d.SaveFcst(ctx, "vilage", 61, 125, 202611092000, []Row{{202611092100, "TMP", "8"}}, now); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(t.TempDir(), "it's.db")
	res, err := d.Backup(ctx, to)
	if err != nil {
		t.Fatal(err)
	}
	if res.SchemaVersion != LatestSchema() || res.Bytes == 0 {
		t.Fatalf("%+v", res)
	}
	cp, err := Open(to)
	if err != nil {
		t.Fatal(err)
	}
	defer cp.Close()
	if ok, _ := cp.HasFcst(ctx, "vilage", 61, 125, 202611092000); !ok {
		t.Fatal("backup missing data")
	}
	if _, err := d.Backup(ctx, to); err == nil {
		t.Fatal("must refuse existing target")
	}
}

func TestDeleteFavoriteRenumbersAfterReorder(t *testing.T) {
	ctx := context.Background()
	d := openTest(t)
	now := time.Now()
	var ids []int64
	u, err := d.CreateUser(ctx, RoleAdmin, now)
	if err != nil {
		t.Fatal(err)
	}
	for i, n := range []string{"a", "b", "c", "d"} {
		f, _, _, err := d.AddFavorite(ctx, u.ID, FavInput{Name: n, NX: 60 + i, NY: 120}, now)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, f.ID)
	}
	_, v, err := d.ReorderFavorites(ctx, u.ID, []int64{ids[3], ids[2], ids[1], ids[0]}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.DeleteFavorite(ctx, u.ID, ids[2]); err != nil { // 두 번째(pos 1) 삭제
		t.Fatal(err)
	}
	fs, v2, _ := d.Favorites(ctx, u.ID)
	if v2 != v+1 || len(fs) != 3 {
		t.Fatalf("v=%d→%d n=%d", v, v2, len(fs))
	}
	want := []int64{ids[3], ids[1], ids[0]}
	for i, f := range fs {
		if f.Pos != i || f.ID != want[i] {
			t.Fatalf("after delete: %+v", fs)
		}
	}
}
