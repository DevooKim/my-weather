package config

import (
	"os"
	"path/filepath"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:8080" || c.DBPath != DefaultDBPath || c.DailyLimit != 10000 || c.StartupBackfill != 23 {
		t.Fatalf("%+v", c)
	}
	if len(c.Grids) != 2 || c.Grids[0] != (Grid{61, 125}) || c.Grids[1] != (Grid{59, 126}) {
		t.Fatalf("grids=%v", c.Grids)
	}
}

func TestGrids(t *testing.T) {
	g, err := ParseGrids(" 61,125 ; 89,130;61,125;")
	if err != nil || len(g) != 2 || g[1] != (Grid{89, 130}) {
		t.Fatalf("%v %v", g, err)
	}
	for _, bad := range []string{"", "61", "0,10", "150,1", "a,b", "1,254"} {
		if _, err := ParseGrids(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
	if _, err := Load(env(map[string]string{"KMA_DAILY_LIMIT": "0"})); err == nil {
		t.Fatal("limit 0 should fail")
	}
}

func TestLoadKey(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	os.WriteFile(p, []byte("other=1\r\napi_key = \"AB%2Bcd\" \r\n"), 0o600)
	c, _ := Load(env(map[string]string{"MYWEATHER_ENV_FILE": p}))
	k, err := c.LoadKey()
	if err != nil || k != "AB%2Bcd" {
		t.Fatalf("%q %v", k, err)
	}
	c, _ = Load(env(map[string]string{"MYWEATHER_ENV_FILE": p, "KMA_SERVICE_KEY": "X"}))
	if k, _ := c.LoadKey(); k != "X" {
		t.Fatal("env var should win")
	}
	c, _ = Load(env(map[string]string{"MYWEATHER_ENV_FILE": filepath.Join(dir, "none")}))
	if _, err := c.LoadKey(); err == nil {
		t.Fatal("want error")
	}
}

func TestHTTPSettings(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil || c.TrustedIPHeader != "X-Forwarded-For" || c.OnDemandBackfill != 23 || c.OnDemandPast != 3 {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = Load(env(map[string]string{"TRUSTED_IP_HEADER": "none", "MYWEATHER_ONDEMAND_BACKFILL_MAX": "99", "MYWEATHER_ONDEMAND_PAST_MAX": "0"}))
	if err != nil || c.TrustedIPHeader != "" || c.OnDemandBackfill != 23 || c.OnDemandPast != 0 {
		t.Fatalf("%+v %v", c, err)
	}
	if len(c.TrustedProxies) != 2 {
		t.Fatalf("default proxies %v", c.TrustedProxies)
	}
	c, err = Load(env(map[string]string{"MYWEATHER_TRUSTED_PROXIES": "127.0.0.0/8,::1/128,10.89.0.0/16", "MYWEATHER_LISTEN": "0.0.0.0:8080"}))
	if err != nil || len(c.TrustedProxies) != 3 || c.Listen != "0.0.0.0:8080" {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := Load(env(map[string]string{"MYWEATHER_TRUSTED_PROXIES": "10.0.0.0/99"})); err == nil {
		t.Fatal("bad cidr")
	}
	if _, err := Load(env(map[string]string{"MYWEATHER_ONDEMAND_PAST_MAX": "-1"})); err == nil {
		t.Fatal("want error")
	}
}
