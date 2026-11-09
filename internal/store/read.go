package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// BaseRows는 발표분 하나(가장 높은 ver)의 값이다.
type BaseRows struct {
	Base int64
	Ver  int
	Rows []Row // (fcst_at, category) 순
}

// FcstRange는 base가 [fromBase, toBase]인 발표분마다 가장 높은 ver의 값 중 fcst_at ≥ fromAt인 것을
// base 오름차순으로 돌려준다(타임라인 읽기 경로, 설계 §4.1).
func (d *DB) FcstRange(ctx context.Context, op string, nx, ny int, fromBase, toBase, fromAt int64) ([]BaseRows, error) {
	rows, err := d.R.QueryContext(ctx, `
		SELECT v.base, v.ver, v.fcst_at, v.category, v.value
		FROM fcst_value v
		JOIN (SELECT base, max(ver) AS ver FROM fcst_version
		      WHERE op=? AND nx=? AND ny=? AND base BETWEEN ? AND ? GROUP BY base) m
		  ON v.base = m.base AND v.ver = m.ver
		WHERE v.op=? AND v.nx=? AND v.ny=? AND v.fcst_at >= ?
		ORDER BY v.base, v.fcst_at, v.category`,
		op, nx, ny, fromBase, toBase, op, nx, ny, fromAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BaseRows
	for rows.Next() {
		var base int64
		var ver int
		var r Row
		if err := rows.Scan(&base, &ver, &r.At, &r.Category, &r.Value); err != nil {
			return nil, err
		}
		if n := len(out); n == 0 || out[n-1].Base != base {
			out = append(out, BaseRows{Base: base, Ver: ver})
		}
		out[len(out)-1].Rows = append(out[len(out)-1].Rows, r)
	}
	return out, rows.Err()
}

// ObsRange는 [from, to] 구간의 실황을 관측 시각별로 돌려준다.
func (d *DB) ObsRange(ctx context.Context, nx, ny int, from, to int64) (map[int64]map[string]string, error) {
	rows, err := d.R.QueryContext(ctx,
		`SELECT obs_at, category, value FROM obs_value WHERE nx=? AND ny=? AND obs_at BETWEEN ? AND ?`, nx, ny, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[string]string{}
	for rows.Next() {
		var at int64
		var c, v string
		if err := rows.Scan(&at, &c, &v); err != nil {
			return nil, err
		}
		m := out[at]
		if m == nil {
			m = map[string]string{}
			out[at] = m
		}
		m[c] = v
	}
	return out, rows.Err()
}

// LatestFcstBase는 (op, nx, ny)에 저장된 가장 늦은 base다. 없으면 0.
func (d *DB) LatestFcstBase(ctx context.Context, op string, nx, ny int) (int64, error) {
	var b sql.NullInt64
	err := d.R.QueryRowContext(ctx, `SELECT max(base) FROM fcst_version WHERE op=? AND nx=? AND ny=?`, op, nx, ny).Scan(&b)
	return b.Int64, err
}

// LatestObs는 (nx, ny)에 저장된 가장 늦은 실황 시각이다. 없으면 0.
func (d *DB) LatestObs(ctx context.Context, nx, ny int) (int64, error) {
	var b sql.NullInt64
	err := d.R.QueryRowContext(ctx, `SELECT max(obs_at) FROM obs_value WHERE nx=? AND ny=?`, nx, ny).Scan(&b)
	return b.Int64, err
}

// GridSeen은 grid_seen에 (nx, ny)가 있는지다(즉시 조회로 한 번이라도 본 격자).
func (d *DB) GridSeen(ctx context.Context, nx, ny int) (bool, error) {
	var x int
	err := d.R.QueryRowContext(ctx, `SELECT 1 FROM grid_seen WHERE nx=? AND ny=?`, nx, ny).Scan(&x)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// TouchGrid는 grid_seen의 first_seen(처음이면)과 last_viewed를 갱신한다.
func (d *DB) TouchGrid(ctx context.Context, nx, ny int, now time.Time) error {
	_, err := d.W.ExecContext(ctx, `
		INSERT INTO grid_seen (nx, ny, first_seen, last_viewed) VALUES (?,?,?,?)
		ON CONFLICT (nx, ny) DO UPDATE SET last_viewed = excluded.last_viewed`,
		nx, ny, now.Unix(), now.Unix())
	return err
}

// Ping은 /healthz용 SELECT 1이다.
func (d *DB) Ping(ctx context.Context) error {
	var x int
	return d.R.QueryRowContext(ctx, `SELECT 1`).Scan(&x)
}

// SizeBytes는 DB 본 파일 크기(page_count × page_size)다.
func (d *DB) SizeBytes(ctx context.Context) (int64, error) {
	var pc, ps int64
	if err := d.R.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pc); err != nil {
		return 0, err
	}
	if err := d.R.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&ps); err != nil {
		return 0, err
	}
	return pc * ps, nil
}
