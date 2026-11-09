package store

import (
	"context"
	"database/sql"
	"time"

	"myweather/internal/kst"
)

// Call은 kma_call 한 줄이다.
type Call struct {
	CalledAt   time.Time
	Op         string
	NX, NY     int
	Base       int64
	Trigger    string
	HTTPStatus int
	ResultCode string
	NItems     int
	TotalCount int
	Ms         int64
	Outcome    string
	Err        string // 호출자가 키를 가린 뒤 넣는다
}

func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// LogCall은 호출 로그를 추가한다. day는 호출 시각의 KST 날짜다.
func (d *DB) LogCall(ctx context.Context, c Call) error {
	_, err := d.W.ExecContext(ctx,
		`INSERT INTO kma_call (called_at, day, op, nx, ny, base, trigger, http_status, result_code, n_items, total_count, ms, outcome, err)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.CalledAt.Unix(), kst.Day(c.CalledAt), c.Op, c.NX, c.NY, c.Base, c.Trigger, c.HTTPStatus,
		nullStr(c.ResultCode), c.NItems, c.TotalCount, c.Ms, nullStr(c.Outcome), nullStr(c.Err))
	return err
}

// CallCountsByOp는 KST 날짜 day의 오퍼레이션별 호출 수다(예산 복구용).
func (d *DB) CallCountsByOp(ctx context.Context, day int64) (map[string]int, error) {
	rows, err := d.R.QueryContext(ctx, `SELECT op, count(*) FROM kma_call WHERE day=? GROUP BY op`, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var op string
		var n int
		if err := rows.Scan(&op, &n); err != nil {
			return nil, err
		}
		out[op] = n
	}
	return out, rows.Err()
}
