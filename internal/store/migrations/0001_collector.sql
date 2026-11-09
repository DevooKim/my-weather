-- 수집기 단계 테이블 (설계 §4.1, §4.2). 즐겨찾기·알림·인증 테이블은 다음 단계 마이그레이션에서 추가한다.

-- 발표분 버전 (헤더). 같은 (op, nx, ny, base)는 내용이 바뀔 때만 ver+1.
CREATE TABLE fcst_version (
  op          TEXT    NOT NULL,       -- 'vilage' | 'ultra'
  nx          INTEGER NOT NULL,
  ny          INTEGER NOT NULL,
  base        INTEGER NOT NULL,       -- KST YYYYMMDDHHMM (초단기는 hh30)
  ver         INTEGER NOT NULL,
  hash        BLOB    NOT NULL,       -- SHA-256 앞 16바이트
  n_rows      INTEGER NOT NULL,       -- totalCount와 같음
  fetched_at  INTEGER NOT NULL,       -- unix s, 이 버전을 처음 받은 시각
  checked_at  INTEGER NOT NULL,       -- unix s, 마지막으로 같은 내용을 확인한 시각
  PRIMARY KEY (op, nx, ny, base, ver)
) WITHOUT ROWID;

-- 발표분 값 (원문 그대로)
CREATE TABLE fcst_value (
  op        TEXT    NOT NULL,
  nx        INTEGER NOT NULL,
  ny        INTEGER NOT NULL,
  base      INTEGER NOT NULL,
  ver       INTEGER NOT NULL,
  fcst_at   INTEGER NOT NULL,
  category  TEXT    NOT NULL,
  value     TEXT    NOT NULL,
  PRIMARY KEY (op, nx, ny, base, ver, fcst_at, category)
) WITHOUT ROWID;

-- 실황. 같은 시각을 다시 받으면 값이 같을 때 무시, 다를 때 덮어쓴다(변경은 서버 로그에 경고).
CREATE TABLE obs_value (
  nx         INTEGER NOT NULL,
  ny         INTEGER NOT NULL,
  obs_at     INTEGER NOT NULL,
  category   TEXT    NOT NULL,
  value      TEXT    NOT NULL,
  fetched_at INTEGER NOT NULL,
  PRIMARY KEY (nx, ny, obs_at, category)
) WITHOUT ROWID;

-- 격자 수집 상태 (즉시 조회 단계에서 사용. 이번 단계는 테이블만 만든다)
CREATE TABLE grid_seen (
  nx INTEGER NOT NULL,
  ny INTEGER NOT NULL,
  first_seen  INTEGER NOT NULL,
  last_viewed INTEGER NOT NULL,
  obs_backfilled_to INTEGER,
  PRIMARY KEY (nx, ny)
) WITHOUT ROWID;

-- KMA 호출 로그
CREATE TABLE kma_call (
  id          INTEGER PRIMARY KEY,
  called_at   INTEGER NOT NULL,       -- unix s
  day         INTEGER NOT NULL,       -- KST YYYYMMDD (예산 집계용)
  op          TEXT    NOT NULL,       -- getUltraSrtNcst | getUltraSrtFcst | getVilageFcst
  nx INTEGER, ny INTEGER,
  base        INTEGER NOT NULL,
  trigger     TEXT    NOT NULL,       -- schedule | retry | refetch | backfill | ondemand | startup
  http_status INTEGER,                -- 0 = 네트워크 오류
  result_code TEXT,
  n_items     INTEGER,
  total_count INTEGER,
  ms          INTEGER NOT NULL,
  outcome     TEXT,                   -- new_version | same | failed
  err         TEXT                    -- 실패 시 본문 앞 500자 (serviceKey는 '***')
);
CREATE INDEX kma_call_day ON kma_call (day, op);
