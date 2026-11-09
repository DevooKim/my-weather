package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// 알림 종류
const (
	AlertSoon = "soon"
	AlertEve  = "eve"
)

// Favorite는 서버 즐겨찾기 한 줄과 알림 설정이다.
type Favorite struct {
	ID        int64
	Name      string
	Region    string // 없으면 ""
	NX, NY    int
	Pos       int
	CreatedAt int64
	Soon, Eve bool
}

// FavInput은 추가·합치기 요청의 장소다.
type FavInput struct {
	Name   string
	Region string
	NX, NY int
}

// ErrNotFound는 없는 행이다.
var ErrNotFound = errors.New("not found")

// ErrConflict는 version이 맞지 않거나 목록이 바뀐 경우다.
var ErrConflict = errors.New("conflict")

type queryer interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

func getMeta(ctx context.Context, q queryer, k string) (string, bool, error) {
	var v string
	err := q.QueryRowContext(ctx, `SELECT v FROM meta WHERE k=?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func setMeta(ctx context.Context, q queryer, k, v string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO meta (k, v) VALUES (?, ?) ON CONFLICT (k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}

// Meta는 meta 테이블 값이다.
func (d *DB) Meta(ctx context.Context, k string) (string, bool, error) { return getMeta(ctx, d.R, k) }

// MetaSetIfAbsent는 k가 없을 때만 v를 넣고, 최종 값을 돌려준다.
func (d *DB) MetaSetIfAbsent(ctx context.Context, k, v string) (string, error) {
	if _, err := d.W.ExecContext(ctx, `INSERT INTO meta (k, v) VALUES (?, ?) ON CONFLICT (k) DO NOTHING`, k, v); err != nil {
		return "", err
	}
	got, _, err := getMeta(ctx, d.W, k)
	return got, err
}

const metaFavVersion = "favorites_version"

func favVersion(ctx context.Context, q queryer) (int64, error) {
	v, ok, err := getMeta(ctx, q, metaFavVersion)
	if err != nil || !ok {
		return 0, err
	}
	return strconv.ParseInt(v, 10, 64)
}

func bumpFavVersion(ctx context.Context, q queryer) (int64, error) {
	v, err := favVersion(ctx, q)
	if err != nil {
		return 0, err
	}
	v++
	return v, setMeta(ctx, q, metaFavVersion, strconv.FormatInt(v, 10))
}

func listFavorites(ctx context.Context, q queryer) ([]Favorite, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT f.id, f.name, coalesce(f.region, ''), f.nx, f.ny, f.pos, f.created_at,
		       coalesce((SELECT enabled FROM alert_pref WHERE favorite_id=f.id AND kind='soon'), 0),
		       coalesce((SELECT enabled FROM alert_pref WHERE favorite_id=f.id AND kind='eve'), 0)
		FROM favorite f ORDER BY f.pos, f.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Favorite{}
	for rows.Next() {
		var f Favorite
		if err := rows.Scan(&f.ID, &f.Name, &f.Region, &f.NX, &f.NY, &f.Pos, &f.CreatedAt, &f.Soon, &f.Eve); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Favorites는 목록(순서대로)과 목록 version이다.
func (d *DB) Favorites(ctx context.Context) ([]Favorite, int64, error) {
	tx, err := d.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	fs, err := listFavorites(ctx, tx)
	if err != nil {
		return nil, 0, err
	}
	v, err := favVersion(ctx, tx)
	return fs, v, err
}

// Favorite는 id 하나다. 없으면 ErrNotFound.
func (d *DB) Favorite(ctx context.Context, id int64) (Favorite, error) {
	fs, _, err := d.Favorites(ctx)
	if err != nil {
		return Favorite{}, err
	}
	for _, f := range fs {
		if f.ID == id {
			return f, nil
		}
	}
	return Favorite{}, ErrNotFound
}

// FavoriteGrids는 즐겨찾기 격자 목록(중복 없음)이다. 수집 대상 계산용.
func (d *DB) FavoriteGrids(ctx context.Context) ([][2]int, error) {
	rows, err := d.R.QueryContext(ctx, `SELECT DISTINCT nx, ny FROM favorite ORDER BY nx, ny`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]int
	for rows.Next() {
		var g [2]int
		if err := rows.Scan(&g[0], &g[1]); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// findSame는 같은 장소(region이 있으면 region, 없으면 nx·ny·name)의 id다. 없으면 0.
func findSame(ctx context.Context, q queryer, in FavInput) (int64, error) {
	var id int64
	var err error
	if in.Region != "" {
		err = q.QueryRowContext(ctx, `SELECT id FROM favorite WHERE region=?`, in.Region).Scan(&id)
	} else {
		err = q.QueryRowContext(ctx, `SELECT id FROM favorite WHERE region IS NULL AND nx=? AND ny=? AND name=?`, in.NX, in.NY, in.Name).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func insertFav(ctx context.Context, q queryer, in FavInput, now time.Time) (int64, error) {
	var pos int
	if err := q.QueryRowContext(ctx, `SELECT coalesce(max(pos)+1, 0) FROM favorite`).Scan(&pos); err != nil {
		return 0, err
	}
	var region any
	if in.Region != "" {
		region = in.Region
	}
	res, err := q.ExecContext(ctx, `INSERT INTO favorite (name, region, nx, ny, pos, created_at) VALUES (?,?,?,?,?,?)`,
		in.Name, region, in.NX, in.NY, pos, now.Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AddFavorite는 즐겨찾기를 추가한다. 같은 장소가 있으면 기존 항목을 돌려주고 created=false(멱등).
func (d *DB) AddFavorite(ctx context.Context, in FavInput, now time.Time) (f Favorite, created bool, version int64, err error) {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return f, false, 0, err
	}
	defer tx.Rollback()
	id, err := findSame(ctx, tx, in)
	if err != nil {
		return f, false, 0, err
	}
	if id == 0 {
		if id, err = insertFav(ctx, tx, in, now); err != nil {
			return f, false, 0, err
		}
		created = true
		if version, err = bumpFavVersion(ctx, tx); err != nil {
			return f, false, 0, err
		}
	} else if version, err = favVersion(ctx, tx); err != nil {
		return f, false, 0, err
	}
	fs, err := listFavorites(ctx, tx)
	if err != nil {
		return f, false, 0, err
	}
	if err := tx.Commit(); err != nil {
		return f, false, 0, err
	}
	for _, x := range fs {
		if x.ID == id {
			return x, created, version, nil
		}
	}
	return f, false, 0, ErrNotFound
}

// MergeFavorites는 기기의 목록을 서버 목록 뒤에 (없는 것만, 요청 순서대로) 붙인다.
func (d *DB) MergeFavorites(ctx context.Context, ins []FavInput, now time.Time) (fs []Favorite, version int64, added, existing int, err error) {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	defer tx.Rollback()
	for _, in := range ins {
		id, err := findSame(ctx, tx, in)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		if id != 0 {
			existing++
			continue
		}
		if _, err := insertFav(ctx, tx, in, now); err != nil {
			return nil, 0, 0, 0, err
		}
		added++
	}
	if added > 0 {
		if version, err = bumpFavVersion(ctx, tx); err != nil {
			return nil, 0, 0, 0, err
		}
	} else if version, err = favVersion(ctx, tx); err != nil {
		return nil, 0, 0, 0, err
	}
	if fs, err = listFavorites(ctx, tx); err != nil {
		return nil, 0, 0, 0, err
	}
	return fs, version, added, existing, tx.Commit()
}

// DeleteFavorite는 즐겨찾기와 알림 설정을 지운다. 예보·실황·알림 기록은 남긴다.
func (d *DB) DeleteFavorite(ctx context.Context, id int64) (int64, error) {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM favorite WHERE id=?`, id)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrNotFound
	}
	// 남은 항목의 pos를 0부터 다시 매긴다(현재 순서 유지)
	rest, err := listFavorites(ctx, tx)
	if err != nil {
		return 0, err
	}
	for i, f := range rest {
		if f.Pos == i {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE favorite SET pos=? WHERE id=?`, i, f.ID); err != nil {
			return 0, err
		}
	}
	v, err := bumpFavVersion(ctx, tx)
	if err != nil {
		return 0, err
	}
	return v, tx.Commit()
}

// RenameFavorite는 표시 이름을 바꾼다. 같은 장소 판정(region 없을 때 nx·ny·name)과 겹치면 ErrConflict.
func (d *DB) RenameFavorite(ctx context.Context, id int64, name string) (Favorite, int64, error) {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return Favorite{}, 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE favorite SET name=? WHERE id=?`, name, id)
	if err != nil {
		if isUniqueErr(err) {
			return Favorite{}, 0, ErrConflict
		}
		return Favorite{}, 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Favorite{}, 0, ErrNotFound
	}
	v, err := bumpFavVersion(ctx, tx)
	if err != nil {
		return Favorite{}, 0, err
	}
	fs, err := listFavorites(ctx, tx)
	if err != nil {
		return Favorite{}, 0, err
	}
	if err := tx.Commit(); err != nil {
		return Favorite{}, 0, err
	}
	for _, f := range fs {
		if f.ID == id {
			return f, v, nil
		}
	}
	return Favorite{}, 0, ErrNotFound
}

func isUniqueErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// ReorderFavorites는 ids 순서로 pos를 다시 매긴다. version이 다르거나 ids가 현재 목록과 다르면
// ErrConflict와 함께 최신 목록·version을 돌려준다.
func (d *DB) ReorderFavorites(ctx context.Context, ids []int64, version int64) ([]Favorite, int64, error) {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	cur, err := listFavorites(ctx, tx)
	if err != nil {
		return nil, 0, err
	}
	v, err := favVersion(ctx, tx)
	if err != nil {
		return nil, 0, err
	}
	same := len(ids) == len(cur)
	if same {
		have := map[int64]bool{}
		for _, f := range cur {
			have[f.ID] = true
		}
		for _, id := range ids {
			if !have[id] {
				same = false
				break
			}
			delete(have, id) // 중복 id 방지
		}
	}
	if v != version || !same {
		return cur, v, ErrConflict
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE favorite SET pos=? WHERE id=?`, i, id); err != nil {
			return nil, 0, err
		}
	}
	if v, err = bumpFavVersion(ctx, tx); err != nil {
		return nil, 0, err
	}
	fs, err := listFavorites(ctx, tx)
	if err != nil {
		return nil, 0, err
	}
	return fs, v, tx.Commit()
}

// AlertPatch는 알림 설정 변경 하나다. nil이면 그대로 둔다.
type AlertPatch struct {
	ID        int64
	Soon, Eve *bool
}

// PatchAlerts는 알림 설정을 바꾼다. 없는 id가 있으면 아무것도 바꾸지 않고 ErrNotFound.
func (d *DB) PatchAlerts(ctx context.Context, ps []AlertPatch) error {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range ps {
		var x int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM favorite WHERE id=?`, p.ID).Scan(&x); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		for kind, v := range map[string]*bool{AlertSoon: p.Soon, AlertEve: p.Eve} {
			if v == nil {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO alert_pref (favorite_id, kind, enabled) VALUES (?,?,?)
				ON CONFLICT (favorite_id, kind) DO UPDATE SET enabled=excluded.enabled`, p.ID, kind, *v); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
