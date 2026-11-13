package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PushSub는 Web Push 구독 하나다.
type PushSub struct {
	ID        int64
	Endpoint  string
	P256dh    string
	Auth      string
	Label     string
	UserID    int64
	CreatedAt int64
	LastOKAt  int64
	FailCount int
}

// UpsertPushSub는 구독을 저장한다. 같은 endpoint면 키·이름만 갱신한다.
func (d *DB) UpsertPushSub(ctx context.Context, s PushSub, now time.Time) (PushSub, error) {
	var userID any
	if s.UserID != 0 {
		userID = s.UserID
	}
	_, err := d.W.ExecContext(ctx, `
		INSERT INTO push_sub (endpoint, p256dh, auth, label, user_id, created_at) VALUES (?,?,?,?,?,?)
		ON CONFLICT (endpoint) DO UPDATE SET p256dh=excluded.p256dh, auth=excluded.auth,
			label=coalesce(excluded.label, push_sub.label), user_id=coalesce(excluded.user_id, push_sub.user_id), fail_count=0`,
		s.Endpoint, s.P256dh, s.Auth, nullStr(s.Label), userID, now.Unix())
	if err != nil {
		return PushSub{}, err
	}
	var out PushSub
	err = d.W.QueryRowContext(ctx, `SELECT id, endpoint, p256dh, auth, coalesce(label,''), created_at, coalesce(last_ok_at,0), fail_count FROM push_sub WHERE endpoint=?`,
		s.Endpoint).Scan(&out.ID, &out.Endpoint, &out.P256dh, &out.Auth, &out.Label, &out.CreatedAt, &out.LastOKAt, &out.FailCount)
	return out, err
}

// PushSubs는 구독 목록이다. endpoint가 비어 있지 않으면 그것 하나만.
func (d *DB) PushSubs(ctx context.Context, endpoint string) ([]PushSub, error) {
	q := `SELECT id, endpoint, p256dh, auth, coalesce(label,''), created_at, coalesce(last_ok_at,0), fail_count FROM push_sub`
	var args []any
	if endpoint != "" {
		q += ` WHERE endpoint=?`
		args = append(args, endpoint)
	}
	rows, err := d.R.QueryContext(ctx, q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSub
	for rows.Next() {
		var s PushSub
		if err := rows.Scan(&s.ID, &s.Endpoint, &s.P256dh, &s.Auth, &s.Label, &s.CreatedAt, &s.LastOKAt, &s.FailCount); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// PushSubCount는 구독 수다.
func (d *DB) PushSubCount(ctx context.Context) (int, error) {
	var n int
	err := d.R.QueryRowContext(ctx, `SELECT count(*) FROM push_sub`).Scan(&n)
	return n, err
}

// DeletePushSub는 endpoint 구독을 지운다(없어도 오류 아님).
func (d *DB) DeletePushSub(ctx context.Context, endpoint string) error {
	_, err := d.W.ExecContext(ctx, `DELETE FROM push_sub WHERE endpoint=?`, endpoint)
	return err
}

// DeleteUserPushSub는 그 사용자의 구독만 지운다.
func (d *DB) DeleteUserPushSub(ctx context.Context, userID int64, endpoint string) error {
	_, err := d.W.ExecContext(ctx, `DELETE FROM push_sub WHERE endpoint=? AND user_id=?`, endpoint, userID)
	return err
}

// PushSubsFor는 한 사용자의 구독이다. endpoint가 있으면 그 하나만.
func (d *DB) PushSubsFor(ctx context.Context, userID int64, endpoint string) ([]PushSub, error) {
	q := `SELECT id, endpoint, p256dh, auth, coalesce(label,''), created_at, coalesce(last_ok_at,0), fail_count FROM push_sub WHERE user_id=?`
	args := []any{userID}
	if endpoint != "" {
		q += ` AND endpoint=?`
		args = append(args, endpoint)
	}
	rows, err := d.R.QueryContext(ctx, q+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSub
	for rows.Next() {
		var s PushSub
		if err := rows.Scan(&s.ID, &s.Endpoint, &s.P256dh, &s.Auth, &s.Label, &s.CreatedAt, &s.LastOKAt, &s.FailCount); err != nil {
			return nil, err
		}
		s.UserID = userID
		out = append(out, s)
	}
	return out, rows.Err()
}

// PushOK는 발송 성공을 기록한다.
func (d *DB) PushOK(ctx context.Context, endpoint string, now time.Time) error {
	_, err := d.W.ExecContext(ctx, `UPDATE push_sub SET last_ok_at=?, fail_count=0 WHERE endpoint=?`, now.Unix(), endpoint)
	return err
}

// PushFail은 실패 횟수를 늘리고 새 값을 돌려준다(없으면 0).
func (d *DB) PushFail(ctx context.Context, endpoint string) (int, error) {
	var n int
	err := d.W.QueryRowContext(ctx, `UPDATE push_sub SET fail_count=fail_count+1 WHERE endpoint=? RETURNING fail_count`, endpoint).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

// LastPushOK는 마지막 발송 성공 시각(unix s)이다. 없으면 0.
func (d *DB) LastPushOK(ctx context.Context) (int64, error) {
	var v sql.NullInt64
	err := d.R.QueryRowContext(ctx, `SELECT max(last_ok_at) FROM push_sub`).Scan(&v)
	return v.Int64, err
}
