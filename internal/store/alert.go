package store

import (
	"context"
	"database/sql"
	"time"
)

// AlertEvent는 alert_event 한 줄이다. 시각 필드는 unix s, 예보 시각은 KST YYYYMMDDHHMM(0 = 없음).
type AlertEvent struct {
	FavoriteID   int64
	Kind         string
	Key          int64
	NX, NY       int
	JudgedAt     int64
	RainFrom     int64
	RainTo       int64
	PopMax       int
	Source       string
	SentAt       int64
	SentN        int
	LastRainSeen int64
	ClosedAt     int64
	Body         string
}

func nullInt(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: v != 0} }

// InsertAlertEvent는 판정 기록을 넣는다. 같은 (즐겨찾기, 종류, 키)가 있으면 넣지 않고 false.
func (d *DB) InsertAlertEvent(ctx context.Context, e AlertEvent) (bool, error) {
	res, err := d.W.ExecContext(ctx, `
		INSERT INTO alert_event (favorite_id, kind, key, nx, ny, judged_at, rain_from, rain_to, pop_max, source, last_rain_seen, body)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
		e.FavoriteID, e.Kind, e.Key, e.NX, e.NY, e.JudgedAt, nullInt(e.RainFrom), nullInt(e.RainTo), e.PopMax, e.Source,
		nullInt(e.LastRainSeen), nullStr(e.Body))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// MarkAlertSent는 발송 결과를 기록한다(n = 성공한 구독 수, 0이면 sent_at을 비워 둔다).
func (d *DB) MarkAlertSent(ctx context.Context, favID int64, kind string, key int64, n int, now time.Time) error {
	var at sql.NullInt64
	if n > 0 {
		at = sql.NullInt64{Int64: now.Unix(), Valid: true}
	}
	_, err := d.W.ExecContext(ctx, `UPDATE alert_event SET sent_at=?, sent_n=? WHERE favorite_id=? AND kind=? AND key=?`, at, n, favID, kind, key)
	return err
}

// LastAlertJudged는 즐겨찾기·종류의 마지막 판정 시각(unix s)이다. 없으면 0.
func (d *DB) LastAlertJudged(ctx context.Context, favID int64, kind string) (int64, error) {
	var v sql.NullInt64
	err := d.R.QueryRowContext(ctx, `SELECT max(judged_at) FROM alert_event WHERE favorite_id=? AND kind=?`, favID, kind).Scan(&v)
	return v.Int64, err
}

// HasAlertEvent는 (즐겨찾기, 종류, 키) 기록이 있는지다.
func (d *DB) HasAlertEvent(ctx context.Context, favID int64, kind string, key int64) (bool, error) {
	var n int
	err := d.R.QueryRowContext(ctx, `SELECT count(*) FROM alert_event WHERE favorite_id=? AND kind=? AND key=?`, favID, kind, key).Scan(&n)
	return n > 0, err
}

// OpenEpisodes는 닫히지 않은 '곧 비' 기록이다.
func (d *DB) OpenEpisodes(ctx context.Context, favID int64) ([]AlertEvent, error) {
	rows, err := d.R.QueryContext(ctx, `SELECT key, judged_at, coalesce(last_rain_seen, judged_at) FROM alert_event
		WHERE favorite_id=? AND kind='soon' AND closed_at IS NULL`, favID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlertEvent
	for rows.Next() {
		e := AlertEvent{FavoriteID: favID, Kind: AlertSoon}
		if err := rows.Scan(&e.Key, &e.JudgedAt, &e.LastRainSeen); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UpdateEpisode는 에피소드의 last_rain_seen(0이면 그대로)과 closed_at(0이면 그대로)을 갱신한다.
func (d *DB) UpdateEpisode(ctx context.Context, favID, key, lastRainSeen, closedAt int64) error {
	_, err := d.W.ExecContext(ctx, `UPDATE alert_event SET last_rain_seen=coalesce(?, last_rain_seen), closed_at=coalesce(?, closed_at)
		WHERE favorite_id=? AND kind='soon' AND key=?`, nullInt(lastRainSeen), nullInt(closedAt), favID, key)
	return err
}

// AlertEvents는 최근 기록이다(테스트·운영 확인용).
func (d *DB) AlertEvents(ctx context.Context, limit int) ([]AlertEvent, error) {
	rows, err := d.R.QueryContext(ctx, `SELECT favorite_id, kind, key, nx, ny, judged_at, coalesce(rain_from,0), coalesce(rain_to,0),
		coalesce(pop_max,0), source, coalesce(sent_at,0), sent_n, coalesce(last_rain_seen,0), coalesce(closed_at,0), coalesce(body,'')
		FROM alert_event ORDER BY judged_at DESC, key DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlertEvent
	for rows.Next() {
		var e AlertEvent
		if err := rows.Scan(&e.FavoriteID, &e.Kind, &e.Key, &e.NX, &e.NY, &e.JudgedAt, &e.RainFrom, &e.RainTo, &e.PopMax, &e.Source,
			&e.SentAt, &e.SentN, &e.LastRainSeen, &e.ClosedAt, &e.Body); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastCollected는 오퍼레이션별 마지막 성공 호출(즉시 조회 제외)의 base와 호출 시각이다.
type LastCollected struct {
	Base     int64
	CalledAt int64
}

// LastCollectedByOp는 정기 수집으로 마지막에 받은 발표분과 받은 시각이다(설정 화면 '마지막 수집').
// sinceDay(KST YYYYMMDD) 이후 호출만 본다(kma_call_day 인덱스).
func (d *DB) LastCollectedByOp(ctx context.Context, ops []string, sinceDay int64) (map[string]LastCollected, error) {
	out := map[string]LastCollected{}
	for _, op := range ops {
		var lc LastCollected
		err := d.R.QueryRowContext(ctx, `
			SELECT base, called_at FROM kma_call
			WHERE day >= ? AND op = ? AND outcome IN ('new_version','same') AND trigger <> 'ondemand'
			ORDER BY base DESC, called_at DESC LIMIT 1`, sinceDay, op).Scan(&lc.Base, &lc.CalledAt)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[op] = lc
	}
	return out, nil
}
