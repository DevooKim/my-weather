// Package config는 환경변수에서 설정을 읽는다.
//
// 수집기·HTTP API·인증·푸시 설정을 둔다.
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"

	"myweather/internal/kma"
	"myweather/internal/ratelimit"
)

// Grid는 수집 대상 격자다.
type Grid struct{ NX, NY int }

func (g Grid) String() string { return fmt.Sprintf("%d,%d", g.NX, g.NY) }

// Config는 프로세스 설정이다.
type Config struct {
	Listen          string // MYWEATHER_LISTEN: HTTP API 주소
	DBPath          string // MYWEATHER_DB
	KMABaseURL      string // MYWEATHER_KMA_BASE_URL
	Grids           []Grid // MYWEATHER_GRIDS
	DailyLimit      int    // KMA_DAILY_LIMIT (오퍼레이션별)
	StartupBackfill int    // MYWEATHER_OBS_BACKFILL_MAX: 시작할 때 격자당 실황 백필 최대 시각 수
	EnvFile         string // MYWEATHER_ENV_FILE: 키를 환경변수로 주지 않았을 때 읽을 파일
	keyFromEnv      string // KMA_SERVICE_KEY

	TrustedIPHeader  string         // TRUSTED_IP_HEADER: 믿는 프록시에서 왔을 때만 보는 클라이언트 IP 헤더. "none"이면 쓰지 않음
	TrustedProxies   []netip.Prefix // MYWEATHER_TRUSTED_PROXIES: 쉼표로 구분한 CIDR (기본 127.0.0.0/8,::1/128)
	OnDemandBackfill int            // MYWEATHER_ONDEMAND_BACKFILL_MAX: 즉시 조회 2단계 실황 백필 최대 시각 수(0~23)
	OnDemandPast     int            // MYWEATHER_ONDEMAND_PAST_MAX: 즉시 조회 3단계 과거 단기 발표분 최대 개수(0~3)

	// 인증 (비우면 인증 API는 503)
	RPID   string // MYWEATHER_RP_ID: 패스키 RP ID (예: weather.example.com)
	Origin string // MYWEATHER_ORIGIN: 화면 Origin. 기본 https://<RP_ID>. 상태 변경 요청의 Origin 검사에도 씀

	// Web Push (키가 없으면 푸시 API는 503, 알림은 판정만 기록)
	VAPIDFile    string // MYWEATHER_VAPID_FILE: VAPID_PUBLIC_KEY=·VAPID_PRIVATE_KEY= 줄이 있는 파일
	VAPIDPublic  string // VAPID_PUBLIC_KEY (파일이 없을 때)
	VAPIDPrivate string // VAPID_PRIVATE_KEY
	VAPIDSubject string // VAPID_SUBJECT: 기본 mailto:khwoo8026@gmail.com
}

// 기본값
const (
	DefaultDBPath       = "./var/myweather.sqlite3"
	DefaultListen       = "127.0.0.1:8080"
	DefaultGrids        = "61,125;59,126" // 역삼1동, 서교동
	DefaultIPHdr        = "X-Forwarded-For"
	DefaultVAPIDSubject = "mailto:khwoo8026@gmail.com"
)

// Load는 getenv(nil이면 os.Getenv)로 설정을 읽는다. 키는 LoadKey로 따로 읽는다.
func Load(getenv func(string) string) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	get := func(k, def string) string {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
		return def
	}
	c := Config{
		Listen:     get("MYWEATHER_LISTEN", DefaultListen),
		DBPath:     get("MYWEATHER_DB", DefaultDBPath),
		KMABaseURL: get("MYWEATHER_KMA_BASE_URL", kma.DefaultBaseURL),
		EnvFile:    get("MYWEATHER_ENV_FILE", ".env"),
		keyFromEnv: strings.TrimSpace(getenv("KMA_SERVICE_KEY")),
	}
	var err error
	if c.Grids, err = ParseGrids(get("MYWEATHER_GRIDS", DefaultGrids)); err != nil {
		return c, err
	}
	if c.DailyLimit, err = atoiMin(get("KMA_DAILY_LIMIT", "10000"), 1, "KMA_DAILY_LIMIT"); err != nil {
		return c, err
	}
	if c.StartupBackfill, err = atoiMin(get("MYWEATHER_OBS_BACKFILL_MAX", "23"), 0, "MYWEATHER_OBS_BACKFILL_MAX"); err != nil {
		return c, err
	}
	if c.StartupBackfill > 23 {
		c.StartupBackfill = 23 // API 보관 기간(24시간) 안의 지난 시각은 최대 23개
	}
	if c.OnDemandBackfill, err = atoiMin(get("MYWEATHER_ONDEMAND_BACKFILL_MAX", "23"), 0, "MYWEATHER_ONDEMAND_BACKFILL_MAX"); err != nil {
		return c, err
	}
	if c.OnDemandBackfill > 23 {
		c.OnDemandBackfill = 23
	}
	if c.OnDemandPast, err = atoiMin(get("MYWEATHER_ONDEMAND_PAST_MAX", "3"), 0, "MYWEATHER_ONDEMAND_PAST_MAX"); err != nil {
		return c, err
	}
	if c.OnDemandPast > 3 {
		c.OnDemandPast = 3
	}
	if c.TrustedProxies, err = ratelimit.ParsePrefixes(get("MYWEATHER_TRUSTED_PROXIES", ratelimit.DefaultTrustedProxies)); err != nil {
		return c, fmt.Errorf("MYWEATHER_TRUSTED_PROXIES: %w", err)
	}
	if len(c.TrustedProxies) == 0 {
		return c, errors.New("MYWEATHER_TRUSTED_PROXIES: empty")
	}
	if err := c.loadAuthPush(get); err != nil {
		return c, err
	}
	c.TrustedIPHeader = get("TRUSTED_IP_HEADER", DefaultIPHdr)
	if strings.EqualFold(c.TrustedIPHeader, "none") {
		c.TrustedIPHeader = ""
	}
	return c, nil
}

func atoiMin(s string, min int, name string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < min {
		return 0, fmt.Errorf("%s: %q is not an integer ≥ %d", name, s, min)
	}
	return n, nil
}

// ParseGrids는 "61,125;59,126" 형식을 읽는다. 중복은 하나로 합친다.
func ParseGrids(s string) ([]Grid, error) {
	var out []Grid
	seen := map[Grid]bool{}
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		xy := strings.Split(part, ",")
		if len(xy) != 2 {
			return nil, fmt.Errorf("MYWEATHER_GRIDS: bad grid %q (want nx,ny)", part)
		}
		nx, err1 := strconv.Atoi(strings.TrimSpace(xy[0]))
		ny, err2 := strconv.Atoi(strings.TrimSpace(xy[1]))
		if err1 != nil || err2 != nil || nx < 1 || nx > 149 || ny < 1 || ny > 253 {
			return nil, fmt.Errorf("MYWEATHER_GRIDS: bad grid %q (1≤nx≤149, 1≤ny≤253)", part)
		}
		g := Grid{nx, ny}
		if !seen[g] {
			seen[g] = true
			out = append(out, g)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("MYWEATHER_GRIDS: no grid")
	}
	return out, nil
}

// LoadKey는 KMA serviceKey를 읽는다. KMA_SERVICE_KEY가 우선이고, 없으면 EnvFile의 api_key를 읽는다.
// 값은 반환만 하고 어디에도 출력하지 않는다.
func (c Config) LoadKey() (string, error) {
	if c.keyFromEnv != "" {
		return c.keyFromEnv, nil
	}
	b, err := os.ReadFile(c.EnvFile)
	if err != nil {
		return "", fmt.Errorf("KMA key: KMA_SERVICE_KEY not set and %s unreadable: %w", c.EnvFile, err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != "api_key" {
			continue
		}
		v = strings.TrimSpace(strings.TrimRight(v, "\r"))
		v = strings.Trim(v, `"'`)
		if v == "" {
			break
		}
		return v, nil
	}
	return "", fmt.Errorf("KMA key: api_key not found in %s", c.EnvFile)
}

func (c *Config) loadAuthPush(get func(k, def string) string) error {
	c.RPID = strings.ToLower(get("MYWEATHER_RP_ID", ""))
	c.Origin = strings.TrimRight(get("MYWEATHER_ORIGIN", ""), "/")
	if c.RPID != "" {
		if strings.ContainsAny(c.RPID, ":/ ") {
			return fmt.Errorf("MYWEATHER_RP_ID: %q must be a bare host name", c.RPID)
		}
		if c.Origin == "" {
			c.Origin = "https://" + c.RPID
		}
		u, err := url.Parse(c.Origin)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" || u.RawQuery != "" {
			return fmt.Errorf("MYWEATHER_ORIGIN: %q must be scheme://host[:port]", c.Origin)
		}
		h := strings.ToLower(u.Hostname())
		if h != c.RPID && !strings.HasSuffix(h, "."+c.RPID) {
			return fmt.Errorf("MYWEATHER_ORIGIN host %q is not RP ID %q or its subdomain", h, c.RPID)
		}
		if u.Scheme == "http" && h != "localhost" {
			return fmt.Errorf("MYWEATHER_ORIGIN: http is allowed only for localhost")
		}
		c.Origin = strings.ToLower(c.Origin)
	} else if c.Origin != "" {
		return errors.New("MYWEATHER_ORIGIN is set but MYWEATHER_RP_ID is empty")
	}

	c.VAPIDSubject = get("VAPID_SUBJECT", DefaultVAPIDSubject)
	c.VAPIDFile = get("MYWEATHER_VAPID_FILE", "")
	if c.VAPIDFile != "" {
		kv, err := ReadKV(c.VAPIDFile)
		if err != nil {
			return fmt.Errorf("MYWEATHER_VAPID_FILE: %w", err)
		}
		c.VAPIDPublic, c.VAPIDPrivate = kv["VAPID_PUBLIC_KEY"], kv["VAPID_PRIVATE_KEY"]
		if c.VAPIDPublic == "" || c.VAPIDPrivate == "" {
			return fmt.Errorf("MYWEATHER_VAPID_FILE %s: VAPID_PUBLIC_KEY and VAPID_PRIVATE_KEY lines required", c.VAPIDFile)
		}
		if v := kv["VAPID_SUBJECT"]; v != "" && get("VAPID_SUBJECT", "") == "" {
			c.VAPIDSubject = v
		}
	} else {
		c.VAPIDPublic, c.VAPIDPrivate = get("VAPID_PUBLIC_KEY", ""), get("VAPID_PRIVATE_KEY", "")
		if (c.VAPIDPublic == "") != (c.VAPIDPrivate == "") {
			return errors.New("VAPID_PUBLIC_KEY and VAPID_PRIVATE_KEY must be set together")
		}
	}
	if !strings.HasPrefix(c.VAPIDSubject, "mailto:") && !strings.HasPrefix(c.VAPIDSubject, "https://") {
		return fmt.Errorf("VAPID_SUBJECT: %q must start with mailto: or https://", c.VAPIDSubject)
	}
	return nil
}

// AuthEnabled는 패스키 인증 설정(RP ID)이 있는지다.
func (c Config) AuthEnabled() bool { return c.RPID != "" }

// PushEnabled는 VAPID 키가 있는지다.
func (c Config) PushEnabled() bool { return c.VAPIDPublic != "" && c.VAPIDPrivate != "" }

// ReadKV는 KEY=VALUE 줄로 된 파일을 읽는다(# 주석, 빈 줄, 따옴표 허용).
func ReadKV(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(strings.TrimPrefix(k, "export "))] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out, nil
}

// LoadAPIHubKey는 기상청 API허브 authKey(안개 시정 자료용)를 읽는다. APIHUB_KEY가 우선이고,
// 없으면 EnvFile의 apihub_key. 없으면 빈 값(안개 정보 없이 동작). 값은 출력하지 않는다.
func (c Config) LoadAPIHubKey() string {
	if v := strings.TrimSpace(os.Getenv("APIHUB_KEY")); v != "" {
		return v
	}
	b, err := os.ReadFile(c.EnvFile)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == "apihub_key" {
			return strings.Trim(strings.TrimSpace(strings.TrimRight(v, "\r")), `"'`)
		}
	}
	return ""
}
