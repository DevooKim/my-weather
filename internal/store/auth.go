package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Passkey는 등록된 패스키 한 줄이다.
type Passkey struct {
	CredID          []byte
	UserID          int64
	PublicKey       []byte
	SignCount       uint32
	AAGUID          []byte
	Transports      string
	BackupEligible  bool
	BackupState     bool
	UserVerified    bool
	AttestationType string
	CloneWarning    bool
	Label           string
	CreatedAt       int64
	LastUsedAt      int64 // 0 = 없음
}

// Passkeys는 등록 순서대로 패스키다. userID가 0이면 전체.
func (d *DB) Passkeys(ctx context.Context, userID int64) ([]Passkey, error) {
	q := `
		SELECT cred_id, public_key, sign_count, aaguid, coalesce(transports,''), coalesce(backup_eligible,0), coalesce(backup_state,0),
		       coalesce(user_verified,0), coalesce(attestation_type,''), clone_warning, coalesce(label,''), coalesce(user_id,0), created_at, coalesce(last_used_at,0)
		FROM passkey`
	var args []any
	if userID != 0 {
		q += ` WHERE user_id=?`
		args = append(args, userID)
	}
	rows, err := d.R.QueryContext(ctx, q+` ORDER BY created_at, cred_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Passkey
	for rows.Next() {
		var p Passkey
		if err := rows.Scan(&p.CredID, &p.PublicKey, &p.SignCount, &p.AAGUID, &p.Transports, &p.BackupEligible, &p.BackupState,
			&p.UserVerified, &p.AttestationType, &p.CloneWarning, &p.Label, &p.UserID, &p.CreatedAt, &p.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PasskeyCount는 패스키 수다.
func (d *DB) PasskeyCount(ctx context.Context, userID int64) (int, error) {
	var n int
	err := d.R.QueryRowContext(ctx, `SELECT count(*) FROM passkey WHERE user_id=?`, userID).Scan(&n)
	return n, err
}

// UpdatePasskeyUse는 로그인 성공 뒤 서명 카운터·플래그·마지막 사용 시각을 갱신한다.
func (d *DB) UpdatePasskeyUse(ctx context.Context, credID []byte, signCount uint32, backupState, cloneWarning bool, now time.Time) error {
	_, err := d.W.ExecContext(ctx, `UPDATE passkey SET sign_count=?, backup_state=?, clone_warning=max(clone_warning, ?), last_used_at=? WHERE cred_id=?`,
		signCount, backupState, cloneWarning, now.Unix(), credID)
	return err
}

// DeletePasskey는 그 사용자의 패스키 하나와 그 패스키로 만든 세션을 지운다. 마지막 하나면 ErrConflict, 없으면 ErrNotFound.
func (d *DB) DeletePasskey(ctx context.Context, userID int64, credID []byte) error {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM passkey WHERE user_id=?`, userID).Scan(&n); err != nil {
		return err
	}
	var x int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM passkey WHERE cred_id=? AND user_id=?`, credID, userID).Scan(&x); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if n <= 1 {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM passkey WHERE cred_id=?`, credID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM session WHERE cred_id=?`, credID); err != nil {
		return err
	}
	return tx.Commit()
}

// Session은 세션 한 줄이다.
type Session struct {
	TokenHash  []byte
	CredID     []byte
	UserID     int64
	Role       string
	Name       string
	CreatedAt  int64
	ExpiresAt  int64
	LastSeenAt int64
}

// NewSession은 세션 행 하나다(Enroll·CreateSession 입력).
type NewSession struct {
	TokenHash []byte
	CredID    []byte
	UserID    int64
	ExpiresAt time.Time
	UserAgent string
}

func insertSession(ctx context.Context, q queryer, s NewSession, now time.Time) error {
	_, err := q.ExecContext(ctx, `INSERT INTO session (token_hash, cred_id, user_id, created_at, expires_at, last_seen_at, user_agent) VALUES (?,?,?,?,?,?,?)`,
		s.TokenHash, s.CredID, s.UserID, now.Unix(), s.ExpiresAt.Unix(), now.Unix(), nullStr(s.UserAgent))
	return err
}

// CreateSession은 세션을 만든다.
func (d *DB) CreateSession(ctx context.Context, s NewSession, now time.Time) error {
	return insertSession(ctx, d.W, s, now)
}

// SessionByHash는 만료되지 않은 세션이다. 없으면 ErrNotFound.
func (d *DB) SessionByHash(ctx context.Context, h []byte, now time.Time) (Session, error) {
	var s Session
	err := d.R.QueryRowContext(ctx, `
		SELECT s.token_hash, s.cred_id, s.user_id, u.role, u.name, s.created_at, s.expires_at, s.last_seen_at
		FROM session s JOIN app_user u ON u.id=s.user_id
		WHERE s.token_hash=? AND s.expires_at>?`,
		h, now.Unix()).Scan(&s.TokenHash, &s.CredID, &s.UserID, &s.Role, &s.Name, &s.CreatedAt, &s.ExpiresAt, &s.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

// TouchSession은 슬라이딩 만료를 연장한다.
func (d *DB) TouchSession(ctx context.Context, h []byte, expires, now time.Time) error {
	_, err := d.W.ExecContext(ctx, `UPDATE session SET expires_at=?, last_seen_at=? WHERE token_hash=?`, expires.Unix(), now.Unix(), h)
	return err
}

// DeleteSession은 세션 하나를 지운다.
func (d *DB) DeleteSession(ctx context.Context, h []byte) error {
	_, err := d.W.ExecContext(ctx, `DELETE FROM session WHERE token_hash=?`, h)
	return err
}

// EnrollToken은 등록 토큰 한 줄이다.
type EnrollToken struct {
	TokenHash      []byte
	ExpiresAt      int64
	UsedAt         int64
	RevokeExisting bool
	Role           string
	UserID         int64
}

// InsertEnrollToken은 등록 토큰을 저장한다(해시만). userID는 이 코드로 등록할 사용자다.
func (d *DB) InsertEnrollToken(ctx context.Context, h []byte, expires time.Time, revoke bool, role string, userID int64, source string, now time.Time) error {
	_, err := d.W.ExecContext(ctx, `INSERT INTO enroll_token (token_hash, created_at, expires_at, revoke_existing, role, user_id, source) VALUES (?,?,?,?,?,?,?)`,
		h, now.Unix(), expires.Unix(), revoke, role, userID, source)
	return err
}

// ValidEnrollToken은 쓰지 않았고 만료되지 않은 토큰이다. 없으면 ErrNotFound.
func (d *DB) ValidEnrollToken(ctx context.Context, h []byte, now time.Time) (EnrollToken, error) {
	var t EnrollToken
	err := d.R.QueryRowContext(ctx, `SELECT token_hash, expires_at, revoke_existing, role, coalesce(user_id,0) FROM enroll_token WHERE token_hash=? AND used_at IS NULL AND expires_at>?`,
		h, now.Unix()).Scan(&t.TokenHash, &t.ExpiresAt, &t.RevokeExisting, &t.Role, &t.UserID)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// Enroll은 한 트랜잭션에서 토큰을 소멸시키고(이미 썼거나 만료면 ErrNotFound),
// revoke_existing이면 기존 패스키·세션을 지우고, 패스키와 세션을 저장한다.
func (d *DB) Enroll(ctx context.Context, tokenHash []byte, p Passkey, s NewSession, now time.Time) (revoked bool, err error) {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var userID int64
	if err := tx.QueryRowContext(ctx, `SELECT revoke_existing, coalesce(user_id,0) FROM enroll_token WHERE token_hash=?`, tokenHash).Scan(&revoked, &userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE enroll_token SET used_at=? WHERE token_hash=? AND used_at IS NULL AND expires_at>?`,
		now.Unix(), tokenHash, now.Unix())
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false, ErrNotFound
	}
	if revoked {
		if _, err := tx.ExecContext(ctx, `DELETE FROM passkey WHERE user_id=?`, userID); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM session WHERE user_id=?`, userID); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO passkey (cred_id, public_key, sign_count, aaguid, transports, backup_eligible, backup_state, user_verified,
		                     attestation_type, label, user_id, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.CredID, p.PublicKey, p.SignCount, p.AAGUID, nullStr(p.Transports), p.BackupEligible, p.BackupState, p.UserVerified,
		nullStr(p.AttestationType), nullStr(p.Label), userID, now.Unix()); err != nil {
		if isUniqueErr(err) {
			return false, ErrConflict
		}
		return false, err
	}
	s.UserID = userID
	if err := insertSession(ctx, tx, s, now); err != nil {
		return false, err
	}
	return revoked, tx.Commit()
}

// CleanupAuth는 만료된 세션과, 쓰였거나 만료된 지 하루가 지난 등록 토큰을 지운다.
func (d *DB) CleanupAuth(ctx context.Context, now time.Time) (sessions, tokens int64, err error) {
	res, err := d.W.ExecContext(ctx, `DELETE FROM session WHERE expires_at<=?`, now.Unix())
	if err != nil {
		return 0, 0, err
	}
	sessions, _ = res.RowsAffected()
	cut := now.Add(-24 * time.Hour).Unix()
	res, err = d.W.ExecContext(ctx, `DELETE FROM enroll_token WHERE (used_at IS NOT NULL AND used_at<?) OR expires_at<?`, cut, cut)
	if err != nil {
		return sessions, 0, err
	}
	tokens, _ = res.RowsAffected()
	return sessions, tokens, nil
}
