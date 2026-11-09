package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Row는 발표분·실황의 값 한 줄이다. 예보면 At=fcst_at, 실황이면 At=obs_at.
type Row struct {
	At       int64
	Category string
	Value    string
}

// 저장 결과 (kma_call.outcome)
const (
	OutcomeNew    = "new_version"
	OutcomeSame   = "same"
	OutcomeFailed = "failed"
)

// ContentHash는 행을 (fcst_at, category, value) 순으로 정렬해 SHA-256 앞 16바이트를 만든다(설계 §4.1).
// 입력 슬라이스는 바꾸지 않는다.
func ContentHash(rows []Row) []byte {
	s := append([]Row(nil), rows...)
	sortRows(s)
	h := sha256.New()
	for _, r := range s {
		fmt.Fprintf(h, "%d\t%s\t%s\n", r.At, r.Category, r.Value)
	}
	return h.Sum(nil)[:16]
}

func sortRows(s []Row) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].At != s[j].At {
			return s[i].At < s[j].At
		}
		if s[i].Category != s[j].Category {
			return s[i].Category < s[j].Category
		}
		return s[i].Value < s[j].Value
	})
}

// FcstVersion은 fcst_version 한 줄이다.
type FcstVersion struct {
	Ver       int
	Hash      []byte
	NRows     int
	FetchedAt int64
	CheckedAt int64
}

// FcstVersions는 (op, nx, ny, base)의 모든 버전을 ver 오름차순으로 돌려준다.
func (d *DB) FcstVersions(ctx context.Context, op string, nx, ny int, base int64) ([]FcstVersion, error) {
	rows, err := d.R.QueryContext(ctx,
		`SELECT ver, hash, n_rows, fetched_at, checked_at FROM fcst_version
		 WHERE op=? AND nx=? AND ny=? AND base=? ORDER BY ver`, op, nx, ny, base)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FcstVersion
	for rows.Next() {
		var v FcstVersion
		if err := rows.Scan(&v.Ver, &v.Hash, &v.NRows, &v.FetchedAt, &v.CheckedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// HasFcst는 (op, nx, ny, base)가 하나라도 저장돼 있는지다.
func (d *DB) HasFcst(ctx context.Context, op string, nx, ny int, base int64) (bool, error) {
	var x int
	err := d.R.QueryRowContext(ctx,
		`SELECT 1 FROM fcst_version WHERE op=? AND nx=? AND ny=? AND base=? LIMIT 1`, op, nx, ny, base).Scan(&x)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// FcstValues는 특정 버전의 값을 (fcst_at, category) 순으로 돌려준다.
func (d *DB) FcstValues(ctx context.Context, op string, nx, ny int, base int64, ver int) ([]Row, error) {
	rows, err := d.R.QueryContext(ctx,
		`SELECT fcst_at, category, value FROM fcst_value
		 WHERE op=? AND nx=? AND ny=? AND base=? AND ver=? ORDER BY fcst_at, category`, op, nx, ny, base, ver)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.At, &r.Category, &r.Value); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveFcst는 발표분을 내용 중복 제거로 저장한다.
// 최신 버전과 해시가 같으면 checked_at만 갱신하고 OutcomeSame, 다르면(또는 처음이면) ver+1로 모든 행을 저장하고 OutcomeNew.
func (d *DB) SaveFcst(ctx context.Context, op string, nx, ny int, base int64, rows []Row, now time.Time) (outcome string, ver int, err error) {
	hash := ContentHash(rows)
	ts := now.Unix()
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	var lastVer int
	var lastHash []byte
	err = tx.QueryRowContext(ctx,
		`SELECT ver, hash FROM fcst_version WHERE op=? AND nx=? AND ny=? AND base=? ORDER BY ver DESC LIMIT 1`,
		op, nx, ny, base).Scan(&lastVer, &lastHash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		err = nil
	case err != nil:
		return "", 0, err
	}
	if lastVer > 0 && bytes.Equal(lastHash, hash) {
		if _, err = tx.ExecContext(ctx,
			`UPDATE fcst_version SET checked_at=? WHERE op=? AND nx=? AND ny=? AND base=? AND ver=?`,
			ts, op, nx, ny, base, lastVer); err != nil {
			return "", 0, err
		}
		if err = tx.Commit(); err != nil {
			return "", 0, err
		}
		return OutcomeSame, lastVer, nil
	}
	ver = lastVer + 1
	if _, err = tx.ExecContext(ctx,
		`INSERT INTO fcst_version (op, nx, ny, base, ver, hash, n_rows, fetched_at, checked_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		op, nx, ny, base, ver, hash, len(rows), ts, ts); err != nil {
		return "", 0, err
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO fcst_value (op, nx, ny, base, ver, fcst_at, category, value) VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		return "", 0, err
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err = stmt.ExecContext(ctx, op, nx, ny, base, ver, r.At, r.Category, r.Value); err != nil {
			return "", 0, fmt.Errorf("insert fcst_value %d %s: %w", r.At, r.Category, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return "", 0, err
	}
	return OutcomeNew, ver, nil
}
