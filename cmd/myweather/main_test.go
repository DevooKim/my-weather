package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"myweather/internal/kma/kmatest"
	"myweather/internal/store"
)

// serve --once를 가짜 KMA 서버로 돌린다(실제 API 호출 없음).
func TestServeOnceAgainstFake(t *testing.T) {
	srv := kmatest.NewServer(nil)
	defer srv.Close()
	dbPath := filepath.Join(t.TempDir(), "sub", "dev.sqlite3")
	t.Setenv("MYWEATHER_DB", dbPath)
	t.Setenv("MYWEATHER_KMA_BASE_URL", srv.URL)
	t.Setenv("KMA_SERVICE_KEY", kmatest.Key)
	t.Setenv("MYWEATHER_GRIDS", "61,125")
	t.Setenv("MYWEATHER_OBS_BACKFILL_MAX", "1")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := runMigrate(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := runServe(context.Background(), log, []string{"--once"}); err != nil {
		t.Fatal(err)
	}
	// 실황 1 + 백필 1 + 초단기 1 + 단기 1
	if n := len(srv.Requests()); n != 4 {
		t.Fatalf("requests=%d", n)
	}
	// 두 번째 실행: 시작 백필을 끄면 이미 받은 발표분은 다시 받지 않으므로 호출 0
	t.Setenv("MYWEATHER_OBS_BACKFILL_MAX", "0")
	if err := runServe(context.Background(), log, []string{"--once"}); err != nil {
		t.Fatal(err)
	}
	if n := len(srv.Requests()); n != 4 {
		t.Fatalf("second run should not call: %d", n)
	}
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	db.R.QueryRow(`SELECT count(*) FROM kma_call WHERE result_code='00'`).Scan(&n)
	if n != 4 {
		t.Fatalf("kma_call ok rows=%d", n)
	}
}

func TestServeRefusesNewerSchemaAndBackup(t *testing.T) {
	srv := kmatest.NewServer(nil)
	defer srv.Close()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite3")
	t.Setenv("MYWEATHER_DB", dbPath)
	t.Setenv("MYWEATHER_KMA_BASE_URL", srv.URL)
	t.Setenv("KMA_SERVICE_KEY", kmatest.Key)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := runMigrate(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := runBackup(context.Background(), []string{"--to", filepath.Join(dir, "b.db")}); err != nil {
		t.Fatal(err)
	}
	db, _ := store.Open(dbPath)
	db.W.Exec("PRAGMA user_version = 99")
	db.Close()
	if err := runServe(context.Background(), log, []string{"--once"}); err == nil {
		t.Fatal("serve must refuse newer schema")
	}
	if err := runMigrate(context.Background(), nil); err == nil {
		t.Fatal("migrate must refuse newer schema")
	}
	if n := len(srv.Requests()); n != 0 {
		t.Fatalf("no KMA calls expected, got %d", n)
	}
}

// serve가 수집기와 HTTP를 함께 띄우고, ctx가 끝나면 정상 종료하는지(0.0.0.0 주소 포함).
func TestServeHTTPStartsAndStops(t *testing.T) {
	srv := kmatest.NewServer(nil)
	defer srv.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	t.Setenv("MYWEATHER_DB", filepath.Join(t.TempDir(), "s.sqlite3"))
	t.Setenv("MYWEATHER_KMA_BASE_URL", srv.URL)
	t.Setenv("KMA_SERVICE_KEY", kmatest.Key)
	t.Setenv("MYWEATHER_GRIDS", "61,125")
	t.Setenv("MYWEATHER_OBS_BACKFILL_MAX", "0")
	t.Setenv("MYWEATHER_LISTEN", fmt.Sprintf("0.0.0.0:%d", port))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, log, nil) }()
	var resp *http.Response
	for i := 0; i < 50; i++ {
		resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"ok":true`) {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not stop")
	}
}
