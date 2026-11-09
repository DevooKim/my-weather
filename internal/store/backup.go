package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
)

// BackupResult는 백업 결과다.
type BackupResult struct {
	Path          string
	SchemaVersion int
	Bytes         int64
}

// Backup은 VACUUM INTO로 일관된 사본을 만들고 사본에 PRAGMA integrity_check를 돌린다(배포 D4).
// 서버가 떠 있어도 된다(읽기 트랜잭션 하나로 복사, WAL에만 있는 쓰기도 포함). 대상 파일이 이미 있으면 거부한다.
// 무결성 검사가 실패하면 사본을 지우고 오류를 돌려준다.
func (d *DB) Backup(ctx context.Context, to string) (BackupResult, error) {
	if strings.ContainsAny(to, "?#") {
		return BackupResult{}, fmt.Errorf("backup: path must not contain '?' or '#'")
	}
	if _, err := os.Stat(to); err == nil {
		return BackupResult{}, fmt.Errorf("backup: %s already exists", to)
	} else if !errors.Is(err, os.ErrNotExist) {
		return BackupResult{}, err
	}
	// VACUUM INTO는 문자열 리터럴만 받는다. 작은따옴표를 이스케이프한다.
	lit := "'" + strings.ReplaceAll(to, "'", "''") + "'"
	if _, err := d.W.ExecContext(ctx, "VACUUM INTO "+lit); err != nil {
		return BackupResult{}, fmt.Errorf("backup: vacuum into: %w", err)
	}
	fail := func(err error) (BackupResult, error) {
		os.Remove(to)
		return BackupResult{}, err
	}
	// 사본은 WAL 설정 없이(VACUUM INTO 결과 그대로 한 파일) 읽기 전용으로 연다.
	cp, err := sql.Open("sqlite", "file:"+to+"?mode=ro")
	if err != nil {
		return fail(err)
	}
	var res string
	err = cp.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&res)
	var ver int
	if err == nil {
		err = cp.QueryRowContext(ctx, "PRAGMA user_version").Scan(&ver)
	}
	cp.Close()
	if err != nil {
		return fail(fmt.Errorf("backup: verify: %w", err))
	}
	if res != "ok" {
		return fail(fmt.Errorf("backup: integrity_check: %s", res))
	}
	st, err := os.Stat(to)
	if err != nil {
		return BackupResult{}, err
	}
	return BackupResult{Path: to, SchemaVersion: ver, Bytes: st.Size()}, nil
}
