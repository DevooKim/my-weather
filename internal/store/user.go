package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// User는 패스키로 로그인하는 한 사람이다.
type User struct {
	ID        int64
	Handle    []byte
	Role      string
	Name      string
	CreatedAt int64
}

func ValidRole(role string) bool {
	return role == RoleAdmin || role == RoleMember
}

func displayName(role string) string {
	if role == RoleAdmin {
		return "관리자"
	}
	return "사용자"
}

// CreateUser는 역할에 맞는 사용자를 만든다. handle은 무작위 32바이트다.
func (d *DB) CreateUser(ctx context.Context, role string, now time.Time) (User, error) {
	if !ValidRole(role) {
		return User{}, fmt.Errorf("user: role %q", role)
	}
	var h [32]byte
	if _, err := rand.Read(h[:]); err != nil {
		return User{}, err
	}
	res, err := d.W.ExecContext(ctx, `INSERT INTO app_user (handle, role, name, created_at) VALUES (?,?,?,?)`,
		h[:], role, displayName(role), now.Unix())
	if err != nil {
		return User{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	return User{ID: id, Handle: append([]byte{}, h[:]...), Role: role, Name: displayName(role), CreatedAt: now.Unix()}, nil
}

func scanUser(row interface {
	Scan(dest ...any) error
}) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Handle, &u.Role, &u.Name, &u.CreatedAt)
	return u, err
}

const userCols = `id, handle, role, name, created_at`

// UserByID는 id로 사용자를 찾는다. 없으면 ErrNotFound.
func (d *DB) UserByID(ctx context.Context, id int64) (User, error) {
	u, err := scanUser(d.R.QueryRowContext(ctx, `SELECT `+userCols+` FROM app_user WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// UserByHandle은 WebAuthn user handle로 찾는다. 없으면 ErrNotFound.
func (d *DB) UserByHandle(ctx context.Context, handle []byte) (User, error) {
	u, err := scanUser(d.W.QueryRowContext(ctx, `SELECT `+userCols+` FROM app_user WHERE handle=?`, handle))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// UserByCredID는 패스키가 속한 사용자다. 없으면 ErrNotFound.
func (d *DB) UserByCredID(ctx context.Context, credID []byte) (User, error) {
	u, err := scanUser(d.W.QueryRowContext(ctx, `
		SELECT u.id, u.handle, u.role, u.name, u.created_at
		FROM app_user u JOIN passkey p ON p.user_id=u.id WHERE p.cred_id=?`, credID))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// OldestAdmin은 가장 먼저 만든 관리자다. 없으면 ErrNotFound.
func (d *DB) OldestAdmin(ctx context.Context) (User, error) {
	u, err := scanUser(d.W.QueryRowContext(ctx, `SELECT `+userCols+` FROM app_user WHERE role=? ORDER BY id LIMIT 1`, RoleAdmin))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// backfillUsers는 user_id가 비어 있는 기존 행을 관리자 1명에게 붙인다.
// 이미 붙어 있으면 아무것도 하지 않는다. 관리자가 없으면 meta의 user handle로 만든다.
func (d *DB) backfillUsers(ctx context.Context) error {
	var orphans int
	err := d.W.QueryRowContext(ctx, `
		SELECT (SELECT count(*) FROM passkey WHERE user_id IS NULL)
		     + (SELECT count(*) FROM favorite WHERE user_id IS NULL)
		     + (SELECT count(*) FROM push_sub WHERE user_id IS NULL)
		     + (SELECT count(*) FROM session WHERE user_id IS NULL)`).Scan(&orphans)
	if err != nil || orphans == 0 {
		return err
	}
	admin, err := d.OldestAdmin(ctx)
	if errors.Is(err, ErrNotFound) {
		admin, err = d.createAdminFromMeta(ctx)
	}
	if err != nil {
		return err
	}
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`UPDATE passkey SET user_id=? WHERE user_id IS NULL`,
		`UPDATE favorite SET user_id=? WHERE user_id IS NULL`,
		`UPDATE push_sub SET user_id=? WHERE user_id IS NULL`,
		`UPDATE session SET user_id=? WHERE user_id IS NULL`,
	} {
		if _, err := tx.ExecContext(ctx, q, admin.ID); err != nil {
			return err
		}
	}
	var ver string
	if err := tx.QueryRowContext(ctx, `SELECT v FROM meta WHERE k=?`, metaFavVersion).Scan(&ver); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if ver != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE app_user SET fav_version=? WHERE id=? AND fav_version=0`, ver, admin.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) createAdminFromMeta(ctx context.Context) (User, error) {
	var handle []byte
	v, ok, err := getMeta(ctx, d.W, "webauthn_user_handle")
	if err != nil {
		return User{}, err
	}
	if ok {
		handle, err = base64.RawURLEncoding.DecodeString(v)
		if err != nil || len(handle) == 0 {
			handle = nil
		}
	}
	if len(handle) == 0 {
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			return User{}, err
		}
		handle = b[:]
	}
	now := time.Now().Unix()
	res, err := d.W.ExecContext(ctx, `INSERT INTO app_user (handle, role, name, created_at) VALUES (?,?,?,?)`,
		handle, RoleAdmin, displayName(RoleAdmin), now)
	if err != nil {
		return User{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	return User{ID: id, Handle: handle, Role: RoleAdmin, Name: displayName(RoleAdmin), CreatedAt: now}, nil
}
