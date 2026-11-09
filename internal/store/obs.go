package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ObsChange는 같은 관측 시각을 다시 받았을 때 값이 바뀐 요소다.
type ObsChange struct {
	Category string
	Old, New string
}

// SaveObs는 실황 한 시각을 저장한다.
// 새 요소는 넣고, 값이 같으면 무시하고, 다르면 덮어쓴다(바뀐 목록을 돌려줘 호출자가 로그를 남긴다).
// 넣거나 바꾼 행이 있으면 OutcomeNew, 없으면 OutcomeSame.
func (d *DB) SaveObs(ctx context.Context, nx, ny int, obsAt int64, rows []Row, now time.Time) (outcome string, changes []ObsChange, err error) {
	ts := now.Unix()
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return "", nil, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	wrote := false
	for _, r := range rows {
		var old string
		err = tx.QueryRowContext(ctx,
			`SELECT value FROM obs_value WHERE nx=? AND ny=? AND obs_at=? AND category=?`,
			nx, ny, obsAt, r.Category).Scan(&old)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err = tx.ExecContext(ctx,
				`INSERT INTO obs_value (nx, ny, obs_at, category, value, fetched_at) VALUES (?,?,?,?,?,?)`,
				nx, ny, obsAt, r.Category, r.Value, ts); err != nil {
				return "", nil, err
			}
			wrote = true
		case err != nil:
			return "", nil, err
		case old != r.Value:
			if _, err = tx.ExecContext(ctx,
				`UPDATE obs_value SET value=?, fetched_at=? WHERE nx=? AND ny=? AND obs_at=? AND category=?`,
				r.Value, ts, nx, ny, obsAt, r.Category); err != nil {
				return "", nil, err
			}
			changes = append(changes, ObsChange{r.Category, old, r.Value})
			wrote = true
		}
	}
	if err = tx.Commit(); err != nil {
		return "", nil, err
	}
	if wrote {
		return OutcomeNew, changes, nil
	}
	return OutcomeSame, nil, nil
}

// ObsTimes는 [from, to] 구간에서 실황이 저장된 관측 시각 집합이다.
func (d *DB) ObsTimes(ctx context.Context, nx, ny int, from, to int64) (map[int64]bool, error) {
	rows, err := d.R.QueryContext(ctx,
		`SELECT DISTINCT obs_at FROM obs_value WHERE nx=? AND ny=? AND obs_at BETWEEN ? AND ?`, nx, ny, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var t int64
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out[t] = true
	}
	return out, rows.Err()
}

// ObsValues는 한 관측 시각의 값을 돌려준다.
func (d *DB) ObsValues(ctx context.Context, nx, ny int, obsAt int64) (map[string]string, error) {
	rows, err := d.R.QueryContext(ctx,
		`SELECT category, value FROM obs_value WHERE nx=? AND ny=? AND obs_at=?`, nx, ny, obsAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var c, v string
		if err := rows.Scan(&c, &v); err != nil {
			return nil, err
		}
		out[c] = v
	}
	return out, rows.Err()
}
