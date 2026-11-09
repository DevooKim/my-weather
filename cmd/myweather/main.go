// Command myweather는 my-weather 단일 바이너리다.
//
// 하위 명령:
//
//	myweather migrate          DB 스키마를 최신으로 올린다
//	myweather serve [--once]   수집기·HTTP API(MYWEATHER_LISTEN)·알림을 함께 실행한다. --once는 수집만 한 번 하고 끝낸다
//	myweather enroll-token [--ttl 10m] [--revoke-existing]   일회용 패스키 등록 토큰 발급 (KMA 키 불필요)
//	myweather vapid-keygen [--out FILE]   VAPID 키 쌍 출력 (--out이면 0600 파일로 저장, 이미 있으면 거부)
//	myweather backup --to P    VACUUM INTO로 온라인 백업 + integrity_check
//	myweather version          버전 출력 (빌드 때 -ldflags "-X main.version=..."로 주입)
//
// DB의 user_version이 이 바이너리가 아는 버전보다 높으면 migrate·serve는 시작을 거부한다.
//
// 설정은 환경변수로 받는다(internal/config 참고).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"myweather/internal/alert"
	"myweather/internal/auth"
	"myweather/internal/budget"
	"myweather/internal/collector"
	"myweather/internal/config"
	"myweather/internal/grid"
	"myweather/internal/httpapi"
	"myweather/internal/kma"
	"myweather/internal/kst"
	"myweather/internal/push"
	"myweather/internal/ratelimit"
	"myweather/internal/store"
)

// version은 빌드 때 -ldflags "-X main.version=v1.0.0"으로 주입한다.
var version = "dev"

func usage() {
	fmt.Fprintln(os.Stderr, `usage: myweather <command> [flags]

commands:
  migrate          apply embedded SQL migrations to the DB
  serve [--once]   run the KMA collector, HTTP API and alerts (--once: collect due work once and exit, no HTTP)
  enroll-token [--ttl 10m] [--revoke-existing]
                   issue a one-time passkey enrollment token (max 24h; --revoke-existing deletes old passkeys/sessions on use)
  vapid-keygen [--out FILE]
                   generate a VAPID key pair (stdout, or FILE with mode 0600; refuses to overwrite)
  backup --to P    online backup (VACUUM INTO) + integrity_check
  version          print version

environment:
  MYWEATHER_DB                DB file path (default ./var/myweather.sqlite3)
  MYWEATHER_GRIDS             grids "nx,ny;nx,ny" (default 61,125;59,126)
  MYWEATHER_LISTEN            HTTP listen address (default 127.0.0.1:8080)
  TRUSTED_IP_HEADER           client IP header, trusted only from MYWEATHER_TRUSTED_PROXIES (default X-Forwarded-For; "none" disables)
  MYWEATHER_TRUSTED_PROXIES   comma-separated proxy CIDRs (default 127.0.0.0/8,::1/128)
  MYWEATHER_ONDEMAND_BACKFILL_MAX  on-demand ncst backfill hours for non-collected grids, 0-23 (default 23)
  MYWEATHER_ONDEMAND_PAST_MAX      on-demand past vilage bases for non-collected grids, 0-3 (default 3)
  KMA_SERVICE_KEY             data.go.kr serviceKey, encoded (default: api_key in MYWEATHER_ENV_FILE)
  MYWEATHER_ENV_FILE          file to read api_key from when KMA_SERVICE_KEY is unset (default .env)
  MYWEATHER_KMA_BASE_URL      KMA endpoint (default apis.data.go.kr)
  KMA_DAILY_LIMIT             daily calls per operation (default 10000)
  MYWEATHER_OBS_BACKFILL_MAX  startup ncst backfill hours per grid, 0-23 (default 23)
  MYWEATHER_RP_ID             passkey RP ID, e.g. weather.example.com (unset: auth/admin API return 503)
  MYWEATHER_ORIGIN            web origin for WebAuthn and Origin checks (default https://<RP_ID>)
  MYWEATHER_VAPID_FILE        file with VAPID_PUBLIC_KEY= and VAPID_PRIVATE_KEY= lines (output of vapid-keygen)
  VAPID_PUBLIC_KEY, VAPID_PRIVATE_KEY  VAPID keys when MYWEATHER_VAPID_FILE is unset (unset: push disabled)
  VAPID_SUBJECT               push contact (default mailto:khwoo8026@gmail.com)`)
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "migrate":
		err = runMigrate(ctx, os.Args[2:])
	case "serve":
		err = runServe(ctx, log, os.Args[2:])
	case "backup":
		err = runBackup(ctx, os.Args[2:])
	case "enroll-token":
		err = runEnrollToken(ctx, os.Args[2:])
	case "vapid-keygen":
		err = runVAPIDKeygen(os.Args[2:])
	case "version", "--version":
		fmt.Println(version)
		return
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Error("myweather: "+os.Args[1]+" failed", "err", err)
		os.Exit(1)
	}
}

func openDB(ctx context.Context, path string) (*store.DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := store.Open(path)
	if err != nil {
		return nil, err
	}
	if _, _, err := db.Migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func runMigrate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	fs.Parse(args)
	cfg, err := config.Load(nil)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return err
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	from, to, err := db.Migrate(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("%s: schema %d → %d\n", cfg.DBPath, from, to)
	return nil
}

func runServe(ctx context.Context, log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	once := fs.Bool("once", false, "run due collection work once and exit (no retries)")
	fs.Parse(args)

	cfg, err := config.Load(nil)
	if err != nil {
		return err
	}
	key, err := cfg.LoadKey()
	if err != nil {
		return err
	}
	db, err := openDB(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	b := budget.New(cfg.DailyLimit, kst.Now, log)
	today := kst.Day(kst.Now())
	counts, err := db.CallCountsByOp(ctx, today)
	if err != nil {
		return err
	}
	b.Restore(today, counts)

	api := kma.New(cfg.KMABaseURL, key, kma.Options{})
	grids := make([]string, len(cfg.Grids))
	for i, g := range cfg.Grids {
		grids[i] = g.String()
	}
	log.Info("myweather: starting", "version", version,
		"db", cfg.DBPath, "grids", strings.Join(grids, ";"), "daily_limit_per_op", cfg.DailyLimit,
		"startup_backfill", cfg.StartupBackfill, "once", *once, "calls_today", counts)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var sender *push.Sender
	if cfg.PushEnabled() {
		if sender, err = push.New(push.Options{DB: db, PublicKey: cfg.VAPIDPublic, PrivateKey: cfg.VAPIDPrivate, Subject: cfg.VAPIDSubject, Log: log}); err != nil {
			return err
		}
	} else if !*once {
		log.Warn("myweather: VAPID keys not set, push disabled (alerts are judged and recorded only)")
	}
	var alerts *alert.Engine
	if !*once {
		opt := alert.Options{DB: db, Log: log}
		if sender != nil {
			opt.Push = sender
		}
		alerts = alert.New(opt)
	}
	colOpt := collector.Options{
		Grids: cfg.Grids, StartupBackfill: cfg.StartupBackfill, Log: log,
		Context: ctx, OnDemandBackfill: cfg.OnDemandBackfill, OnDemandPast: cfg.OnDemandPast,
	}
	if alerts != nil {
		colOpt.OnUltra = alerts.OnUltra
	}
	c := collector.New(db, api, b, colOpt)
	favGrids, err := db.FavoriteGrids(ctx)
	if err != nil {
		return err
	}
	c.SetExtraGrids(favGrids)
	if len(favGrids) > 0 {
		log.Info("myweather: favorite grids added to collection", "favorite_grids", len(favGrids), "total_grids", len(c.Grids()))
	}
	if *once {
		n := c.Step(ctx)
		log.Info("myweather: once done", "calls", n, "budget", b.Snapshot().ByOp)
		return nil
	}

	regions, err := grid.Load()
	if err != nil {
		return err
	}
	var au *auth.Auth
	if cfg.AuthEnabled() {
		if au, err = auth.New(auth.Options{DB: db, RPID: cfg.RPID, Origin: cfg.Origin, Log: log}); err != nil {
			return err
		}
		log.Info("myweather: passkey auth enabled", "rp_id", cfg.RPID, "origin", cfg.Origin)
	} else {
		log.Warn("myweather: MYWEATHER_RP_ID not set, login and admin API disabled (503)")
	}
	hopt := httpapi.Options{
		DB: db, DBPath: cfg.DBPath, Collector: c, Budget: b, Regions: regions, Version: version, Log: log,
		IP: &ratelimit.ClientIP{TrustHeader: cfg.TrustedIPHeader, Trusted: cfg.TrustedProxies, Log: log},
	}
	if au != nil {
		hopt.Auth = au
	}
	if sender != nil {
		hopt.Push = sender
	}
	api2 := httpapi.New(hopt)
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           api2.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	log.Info("myweather: http listening", "addr", ln.Addr().String(), "trusted_ip_header", cfg.TrustedIPHeader,
		"trusted_proxies", fmt.Sprint(cfg.TrustedProxies),
		"ondemand_backfill", cfg.OnDemandBackfill, "ondemand_past", cfg.OnDemandPast)

	errc := make(chan error, 2)
	go alerts.Run(ctx)
	if au != nil {
		go au.RunCleanup(ctx)
	}
	go func() { errc <- c.Run(ctx) }()
	go func() { errc <- srv.Serve(ln) }()
	err = <-errc
	cancel()
	sctx, scancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer scancel()
	if e := srv.Shutdown(sctx); e != nil && err == nil {
		err = e
	}
	c.WaitBackground()
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func runBackup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	to := fs.String("to", "", "backup file path (must not exist)")
	fs.Parse(args)
	if *to == "" {
		return errors.New("backup: --to is required")
	}
	cfg, err := config.Load(nil)
	if err != nil {
		return err
	}
	if _, err := os.Stat(cfg.DBPath); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	res, err := db.Backup(ctx, *to)
	if err != nil {
		return err
	}
	fmt.Printf("%s: ok (schema v%d, %d bytes)\n", res.Path, res.SchemaVersion, res.Bytes)
	return nil
}

func runEnrollToken(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("enroll-token", flag.ExitOnError)
	ttl := fs.Duration("ttl", auth.DefaultTokenTTL, "token lifetime (max 24h)")
	revoke := fs.Bool("revoke-existing", false, "on successful enrollment, delete all existing passkeys and sessions (lost phone)")
	fs.Parse(args)
	cfg, err := config.Load(nil)
	if err != nil {
		return err
	}
	db, err := openDB(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	tok, exp, err := auth.IssueEnrollToken(ctx, db, *ttl, *revoke, kst.Now())
	if err != nil {
		return err
	}
	fmt.Println(tok)
	note := ""
	if *revoke {
		note = "; existing passkeys and sessions will be deleted when it is used"
	}
	fmt.Fprintf(os.Stderr, "one-time token, valid until %s KST (%s)%s\n", exp.Format("2006-01-02 15:04:05"), *ttl, note)
	return nil
}

func runVAPIDKeygen(args []string) error {
	fs := flag.NewFlagSet("vapid-keygen", flag.ExitOnError)
	out := fs.String("out", "", "write to this file (mode 0600, must not exist) instead of stdout")
	fs.Parse(args)
	pub, priv, err := push.GenerateKeys()
	if err != nil {
		return err
	}
	text := "VAPID_PUBLIC_KEY=" + pub + "\nVAPID_PRIVATE_KEY=" + priv + "\n"
	if *out == "" {
		fmt.Print(text)
		return nil
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s: written (public key %s)\n", *out, pub)
	return nil
}
