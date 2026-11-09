-- 3단계 테이블: 즐겨찾기·알림 설정·푸시 구독·알림 이벤트·세션·패스키·등록 토큰 (설계 §4.3~4.5).
-- 설계 DDL과 다른 점은 docs/impl-notes.md 3단계 절에 적었다.

CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT NOT NULL) WITHOUT ROWID;
-- 키: favorites_version, webauthn_user_handle(base64url 32바이트)

CREATE TABLE favorite (
  id         INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL,
  region     TEXT,                    -- 행정구역 코드 (없으면 NULL)
  nx         INTEGER NOT NULL,
  ny         INTEGER NOT NULL,
  pos        INTEGER NOT NULL,
  created_at INTEGER NOT NULL         -- unix s
);
CREATE UNIQUE INDEX favorite_region ON favorite (region) WHERE region IS NOT NULL;
CREATE UNIQUE INDEX favorite_grid_name ON favorite (nx, ny, name) WHERE region IS NULL;
CREATE INDEX favorite_grid ON favorite (nx, ny);

CREATE TABLE alert_pref (
  favorite_id INTEGER NOT NULL REFERENCES favorite(id) ON DELETE CASCADE,
  kind        TEXT    NOT NULL,       -- 'soon' | 'eve'
  enabled     INTEGER NOT NULL,
  PRIMARY KEY (favorite_id, kind)
) WITHOUT ROWID;

-- 설계는 endpoint가 기본 키였지만 응답에 id가 필요해서 id를 두고 endpoint는 UNIQUE로 했다.
CREATE TABLE push_sub (
  id          INTEGER PRIMARY KEY,
  endpoint    TEXT    NOT NULL UNIQUE,
  p256dh      TEXT    NOT NULL,
  auth        TEXT    NOT NULL,
  label       TEXT,
  created_at  INTEGER NOT NULL,
  last_ok_at  INTEGER,
  fail_count  INTEGER NOT NULL DEFAULT 0
);

-- 알림 판정 기록. soon은 비 에피소드(같은 비) 기록을 겸한다.
CREATE TABLE alert_event (
  favorite_id    INTEGER NOT NULL,    -- FK 없음: 즐겨찾기를 지워도 기록은 남김
  kind           TEXT    NOT NULL,    -- 'soon' | 'eve'
  key            INTEGER NOT NULL,    -- soon: 예상 시작 시각 YYYYMMDDHHMM / eve: 대상 날짜 YYYYMMDD
  nx INTEGER NOT NULL, ny INTEGER NOT NULL,
  judged_at      INTEGER NOT NULL,    -- unix s. 쿨다운 기준 (보내지 못했어도 판정 시각)
  rain_from      INTEGER,             -- 예상 시작 YYYYMMDDHHMM
  rain_to        INTEGER,             -- 예상 끝
  pop_max        INTEGER,
  source         TEXT    NOT NULL,    -- 'ultra:202611091330' / 'vilage:202611092000'
  sent_at        INTEGER,             -- NULL = 판정만 하고 한 건도 못 보냄
  sent_n         INTEGER NOT NULL DEFAULT 0,
  last_rain_seen INTEGER,             -- unix s. 비 예보·관측을 마지막으로 본 시각
  closed_at      INTEGER,             -- 에피소드 종료 (비 없음이 3시간 넘게 이어짐)
  body           TEXT,
  PRIMARY KEY (favorite_id, kind, key)
) WITHOUT ROWID;
CREATE INDEX alert_event_recent ON alert_event (favorite_id, kind, judged_at);

CREATE TABLE passkey (
  cred_id         BLOB PRIMARY KEY,
  public_key      BLOB    NOT NULL,   -- COSE
  sign_count      INTEGER NOT NULL,
  aaguid          BLOB,
  transports      TEXT,               -- 'internal,hybrid'
  backup_eligible INTEGER,
  backup_state    INTEGER,
  user_verified   INTEGER,
  attestation_type TEXT,
  clone_warning   INTEGER NOT NULL DEFAULT 0,
  label           TEXT,
  created_at      INTEGER NOT NULL,
  last_used_at    INTEGER
) WITHOUT ROWID;

CREATE TABLE session (
  token_hash   BLOB PRIMARY KEY,      -- SHA-256(쿠키 값)
  cred_id      BLOB,                  -- 어떤 패스키로 로그인했는지
  created_at   INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  user_agent   TEXT
) WITHOUT ROWID;

CREATE TABLE enroll_token (
  token_hash      BLOB PRIMARY KEY,
  created_at      INTEGER NOT NULL,
  expires_at      INTEGER NOT NULL,
  used_at         INTEGER,
  revoke_existing INTEGER NOT NULL DEFAULT 0,
  source          TEXT    NOT NULL    -- 'cli'
) WITHOUT ROWID;
