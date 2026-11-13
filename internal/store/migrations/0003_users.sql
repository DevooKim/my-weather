-- 여러 사용자. 기존 패스키·즐겨찾기·세션·푸시는 마이그레이션 뒤 Go backfill이 관리자 1명에게 붙인다.

CREATE TABLE app_user (
  id          INTEGER PRIMARY KEY,
  handle      BLOB    NOT NULL UNIQUE,
  role        TEXT    NOT NULL CHECK (role IN ('admin', 'member')),
  name        TEXT    NOT NULL,
  fav_version INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL
);

ALTER TABLE passkey ADD COLUMN user_id INTEGER REFERENCES app_user(id);
ALTER TABLE session ADD COLUMN user_id INTEGER REFERENCES app_user(id);
ALTER TABLE favorite ADD COLUMN user_id INTEGER REFERENCES app_user(id);
ALTER TABLE push_sub ADD COLUMN user_id INTEGER REFERENCES app_user(id);
ALTER TABLE enroll_token ADD COLUMN role TEXT NOT NULL DEFAULT 'member';
ALTER TABLE enroll_token ADD COLUMN user_id INTEGER REFERENCES app_user(id);

DROP INDEX IF EXISTS favorite_region;
DROP INDEX IF EXISTS favorite_grid_name;
CREATE UNIQUE INDEX favorite_region ON favorite (user_id, region) WHERE region IS NOT NULL;
CREATE UNIQUE INDEX favorite_grid_name ON favorite (user_id, nx, ny, name) WHERE region IS NULL;
CREATE INDEX favorite_user ON favorite (user_id, pos, id);
CREATE INDEX passkey_user ON passkey (user_id);
CREATE INDEX session_user ON session (user_id);
