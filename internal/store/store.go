// Package store는 SQLite 연결, embed된 마이그레이션, 테이블별 저장소를 담당한다.
//
// 쓰기는 연결 1개로 직렬화하고(W), 읽기는 여러 연결(R)로 한다. 드라이버는 순수 Go modernc.org/sqlite.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// DB는 쓰기용·읽기용 연결 풀 묶음이다.
type DB struct {
	W *sql.DB
	R *sql.DB
}

// 연결마다 적용해야 하는 PRAGMA는 DSN에 넣는다(풀의 모든 연결에 적용되도록).
func dsn(path string, readOnly bool) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	if readOnly {
		q.Add("_pragma", "query_only(1)")
	} else {
		q.Set("_txlock", "immediate")
	}
	return "file:" + path + "?" + q.Encode()
}

// Open은 path의 SQLite 파일을 연다(없으면 만든다). 마이그레이션은 Migrate로 따로 한다.
func Open(path string) (*DB, error) {
	w, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	if err := w.Ping(); err != nil {
		w.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	r, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)
	return &DB{W: w, R: r}, nil
}

// Close는 두 풀을 닫는다.
func (d *DB) Close() error {
	e1 := d.R.Close()
	e2 := d.W.Close()
	if e2 != nil {
		return e2
	}
	return e1
}

type migration struct {
	n    int
	name string
	sql  string
}

func loadMigrations() ([]migration, error) {
	ents, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") || len(name) < 5 {
			continue
		}
		n, err := strconv.Atoi(name[:4])
		if err != nil {
			return nil, fmt.Errorf("migration %s: name must start with NNNN_", name)
		}
		b, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{n, name, string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].n < out[j].n })
	for i, m := range out {
		if m.n != i+1 {
			return nil, fmt.Errorf("migration numbering gap at %s", m.name)
		}
	}
	return out, nil
}

// SchemaVersion은 PRAGMA user_version이다.
func (d *DB) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := d.W.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}

// Migrate는 user_version 뒤의 마이그레이션을 순서대로 한 트랜잭션씩 적용한다.
func (d *DB) Migrate(ctx context.Context) (from, to int, err error) {
	ms, err := loadMigrations()
	if err != nil {
		return 0, 0, err
	}
	from, err = d.SchemaVersion(ctx)
	if err != nil {
		return 0, 0, err
	}
	if from > len(ms) {
		return from, from, fmt.Errorf("db schema version %d is newer than this binary (%d)", from, len(ms))
	}
	to = from
	for _, m := range ms[from:] {
		tx, err := d.W.BeginTx(ctx, nil)
		if err != nil {
			return from, to, err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return from, to, fmt.Errorf("migration %s: %w", m.name, err)
		}
		// user_version은 바인드 파라미터를 받지 않는다.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.n)); err != nil {
			tx.Rollback()
			return from, to, err
		}
		if err := tx.Commit(); err != nil {
			return from, to, err
		}
		to = m.n
	}
	return from, to, nil
}

// LatestSchema는 바이너리에 들어 있는 마지막 마이그레이션 번호다.
func LatestSchema() int {
	ms, err := loadMigrations()
	if err != nil {
		return 0
	}
	return len(ms)
}
