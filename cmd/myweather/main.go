// Command myweather는 my-weather 단일 바이너리다.
//
// 하위 명령(이번 단계):
//
//	myweather migrate          DB 스키마를 최신으로 올린다
//	myweather serve [--once]   수집기를 실행한다(HTTP API는 다음 단계). --once는 지금 할 일만 한 번 하고 끝낸다
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
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"myweather/internal/budget"
	"myweather/internal/collector"
	"myweather/internal/config"
	"myweather/internal/kma"
	"myweather/internal/kst"
	"myweather/internal/store"
)

// version은 빌드 때 -ldflags "-X main.version=v1.0.0"으로 주입한다.
var version = "dev"

func usage() {
	fmt.Fprintln(os.Stderr, `usage: myweather <command> [flags]

commands:
  migrate          apply embedded SQL migrations to the DB
  serve [--once]   run the KMA collector (--once: run due work once and exit)
  backup --to P    online backup (VACUUM INTO) + integrity_check
  version          print version

environment:
  MYWEATHER_DB                DB file path (default ./var/myweather.sqlite3)
  MYWEATHER_GRIDS             grids "nx,ny;nx,ny" (default 61,125;59,126)
  MYWEATHER_LISTEN            HTTP listen address (next stage; default 127.0.0.1:8080)
  KMA_SERVICE_KEY             data.go.kr serviceKey, encoded (default: api_key in MYWEATHER_ENV_FILE)
  MYWEATHER_ENV_FILE          file to read api_key from when KMA_SERVICE_KEY is unset (default .env)
  MYWEATHER_KMA_BASE_URL      KMA endpoint (default apis.data.go.kr)
  KMA_DAILY_LIMIT             daily calls per operation (default 10000)
  MYWEATHER_OBS_BACKFILL_MAX  startup ncst backfill hours per grid, 0-23 (default 23)`)
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
	log.Info("myweather: collector starting", "version", version,
		"db", cfg.DBPath, "grids", strings.Join(grids, ";"), "daily_limit_per_op", cfg.DailyLimit,
		"startup_backfill", cfg.StartupBackfill, "once", *once, "calls_today", counts)

	c := collector.New(db, api, b, collector.Options{Grids: cfg.Grids, StartupBackfill: cfg.StartupBackfill, Log: log})
	if *once {
		n := c.Step(ctx)
		log.Info("myweather: once done", "calls", n, "budget", b.Snapshot().ByOp)
		return nil
	}
	return c.Run(ctx)
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
