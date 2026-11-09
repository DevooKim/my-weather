// Package alert는 '곧 비'·'전날 밤 예고' 판정과 발송, 비 에피소드 기록을 맡는다(기획서 §6, 설계 §7, §8.1 Q6).
//
// '곧 비' (E30, docs/precip-skill-2026-11-09.md §6(2)): 초단기예보 최신 발표분을 정기 수집으로 저장한 직후,
// 그 격자의 '곧 비'가 켜진 즐겨찾기마다 아래를 모두 만족하면 보낸다.
//  1. 발표(hh:30)의 2~4번째 정시(hh+2·hh+3·hh+4시) 중 초단기 PTY ∈ {1,2,3,4}인 시각 t가 있다(이미 지난 시각은 뺌).
//  2. 그 t의 ±1시간 단기예보(저장된 최신 발표분)에 POP ≥ 30 또는 PTY ≠ 0이 있다.
//  3. 최근 3시간(정시 실황 3개) 실황에 강수(PTY ≠ 0 또는 RN1 > 0)가 없다. 실황이 빠진 시각은 무강수로 본다.
//  4. 그 즐겨찾기의 마지막 '곧 비' 판정 뒤 6시간이 지났다.
//
// 열린 에피소드는 막는 조건이 아니라 기록이다(시뮬레이션한 규칙은 3·4번만으로 반복 0회).
// 판정할 때마다 비 예보(1번의 PTY)나 실황 강수가 있으면 last_rain_seen을 갱신하고, 3시간 넘게 없으면 닫는다.
//
// '전날 밤 예고': 매일 22:00(KST). 오늘 20시 발표분(없으면 17시)에서 내일 00~23시 중 PTY ≠ 0 또는 POP ≥ 60인 시각을 찾는다.
// 22:00에 프로세스가 꺼져 있었으면 그날 23:59까지 시작할 때 따라잡는다. (즐겨찾기, 내일 날짜)마다 한 번.
package alert

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"myweather/internal/kst"
	"myweather/internal/push"
	"myweather/internal/store"
)

// Rules는 판정 기준값이다. v1은 읽기 전용(설정 화면에 보여 주기만 함).
type Rules struct {
	SoonPopMin   int           // 단기 POP 게이트 (E30: 30)
	EvePopMin    int           // 전날 밤 예고 POP 기준 (60)
	Cooldown     time.Duration // 마지막 '곧 비' 뒤 쉬는 시간 (6h)
	DryWindow    int           // 최근 실황 무강수 시각 수 (3)
	EpisodeClose time.Duration // 비 없음이 이만큼 이어지면 에피소드 닫음 (3h)
	EveHour      int           // 22
	SoonTTL      time.Duration // 푸시 보관 시간 1h
	EveTTL       time.Duration // 8h
}

// DefaultRules는 E30 권장값이다.
var DefaultRules = Rules{SoonPopMin: 30, EvePopMin: 60, Cooldown: 6 * time.Hour, DryWindow: 3, EpisodeClose: 3 * time.Hour,
	EveHour: 22, SoonTTL: time.Hour, EveTTL: 8 * time.Hour}

// Notifier는 푸시 발송기다(*push.Sender).
type Notifier interface {
	SendAll(ctx context.Context, m push.Message, endpoint string) (push.Report, error)
}

// Options는 엔진 설정이다.
type Options struct {
	DB    *store.DB
	Push  Notifier // nil이면 판정만 기록하고 보내지 않음
	Rules Rules    // 0값이면 DefaultRules
	Now   func() time.Time
	Log   *slog.Logger
}

// Engine은 알림 판정기다.
type Engine struct {
	o     Options
	queue chan ultraJob
	mu    sync.Mutex // 판정은 한 번에 하나
}

type ultraJob struct {
	nx, ny int
	base   int64
}

// New는 엔진을 만든다.
func New(o Options) *Engine {
	if o.Rules == (Rules{}) {
		o.Rules = DefaultRules
	}
	if o.Now == nil {
		o.Now = kst.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return &Engine{o: o, queue: make(chan ultraJob, 64)}
}

// OnUltra는 수집기가 초단기 최신 발표분을 저장했을 때 부른다. 막지 않고 큐에 넣는다.
func (e *Engine) OnUltra(nx, ny int, base int64) {
	select {
	case e.queue <- ultraJob{nx, ny, base}:
	default:
		e.o.Log.Warn("alert: queue full, dropped", "nx", nx, "ny", ny, "base", base)
	}
}

// Run은 ctx가 끝날 때까지 '곧 비' 큐와 매일 22:00 '전날 밤 예고'를 처리한다.
func (e *Engine) Run(ctx context.Context) {
	e.catchUpEve(ctx)
	for {
		now := e.o.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), e.o.Rules.EveHour, 0, 0, 0, kst.Loc)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		// 시계 변경·절전에 대비해 최대 10분마다 다시 계산
		d := min(next.Sub(now), 10*time.Minute)
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case j := <-e.queue:
			t.Stop()
			if err := e.CheckSoon(ctx, j.nx, j.ny, j.base); err != nil && ctx.Err() == nil {
				e.o.Log.Error("alert: soon check failed", "nx", j.nx, "ny", j.ny, "err", err)
			}
		case <-t.C:
			e.catchUpEve(ctx)
		}
	}
}

func (e *Engine) catchUpEve(ctx context.Context) {
	now := e.o.Now()
	if now.Hour() < e.o.Rules.EveHour {
		return
	}
	if err := e.CheckEve(ctx, now); err != nil && ctx.Err() == nil {
		e.o.Log.Error("alert: eve check failed", "err", err)
	}
}

// --- 공통 ---

type grid map[int64]map[string]string // fcst_at → category → value

func loadBase(ctx context.Context, db *store.DB, op string, nx, ny int, base, fromAt int64) (grid, error) {
	bs, err := db.FcstRange(ctx, op, nx, ny, base, base, fromAt)
	if err != nil || len(bs) == 0 {
		return nil, err
	}
	g := grid{}
	for _, r := range bs[len(bs)-1].Rows {
		m := g[r.At]
		if m == nil {
			m = map[string]string{}
			g[r.At] = m
		}
		m[r.Category] = r.Value
	}
	return g, nil
}

func atoi(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func (g grid) pty(t time.Time) int {
	if n, ok := atoi(g[kst.ToInt(t)]["PTY"]); ok {
		return n
	}
	return -1
}

func (g grid) pop(t time.Time) int {
	if n, ok := atoi(g[kst.ToInt(t)]["POP"]); ok {
		return n
	}
	return -1
}

func precipWord(pty int) string {
	switch pty {
	case 2:
		return "비/눈"
	case 3:
		return "눈"
	default:
		return "비"
	}
}

func (e *Engine) favoritesAt(ctx context.Context, nx, ny int, kind string) ([]store.Favorite, error) {
	fs, _, err := e.o.DB.Favorites(ctx)
	if err != nil {
		return nil, err
	}
	var out []store.Favorite
	for _, f := range fs {
		if (nx == 0 || (f.NX == nx && f.NY == ny)) && ((kind == store.AlertSoon && f.Soon) || (kind == store.AlertEve && f.Eve)) {
			out = append(out, f)
		}
	}
	return out, nil
}

func (e *Engine) send(ctx context.Context, f store.Favorite, kind string, key int64, m push.Message) {
	n := 0
	if e.o.Push == nil {
		e.o.Log.Warn("alert: push not configured, recorded only", "fav", f.ID, "kind", kind, "body", m.Body)
	} else {
		rep, err := e.o.Push.SendAll(ctx, m, "")
		if err != nil {
			e.o.Log.Error("alert: send", "fav", f.ID, "kind", kind, "err", err)
		}
		n = rep.Sent
		e.o.Log.Info("alert: sent", "fav", f.ID, "kind", kind, "body", m.Body, "sent", rep.Sent, "failed", rep.Failed, "removed", rep.Removed)
	}
	if err := e.o.DB.MarkAlertSent(ctx, f.ID, kind, key, n, e.o.Now()); err != nil {
		e.o.Log.Error("alert: mark sent", "err", err)
	}
}

// --- 곧 비 ---

// SoonResult는 격자 하나의 '곧 비' 판정 재료다(테스트·로그용).
type SoonResult struct {
	Candidates []time.Time // 1·2번을 만족한 시각
	UltraRain  bool        // 1번(PTY 1~4)만 본 결과. 에피소드 갱신용
	ObsRain    bool        // 최근 실황 강수
	RainTo     time.Time   // 초단기 비 예보의 마지막 시각
	StartPty   int
	PopMax     int
}

// JudgeSoon은 저장된 데이터로 (nx, ny)의 초단기 발표분 base에 대한 판정 재료를 만든다.
func (e *Engine) JudgeSoon(ctx context.Context, nx, ny int, base int64, now time.Time) (SoonResult, error) {
	var r SoonResult
	ug, err := loadBase(ctx, e.o.DB, "ultra", nx, ny, base, 0)
	if err != nil || ug == nil {
		return r, err
	}
	vb, err := e.o.DB.LatestFcstBase(ctx, "vilage", nx, ny)
	if err != nil {
		return r, err
	}
	var vg grid
	if vb > 0 {
		if vg, err = loadBase(ctx, e.o.DB, "vilage", nx, ny, vb, 0); err != nil {
			return r, err
		}
	}
	b := kst.FromInt(base)
	hour0 := b.Truncate(time.Hour) // hh:00 (hh:30 발표)
	for i := 2; i <= 4; i++ {
		t := hour0.Add(time.Duration(i) * time.Hour)
		p := ug.pty(t)
		if p < 1 || p > 4 {
			continue
		}
		if !t.After(now) {
			continue
		}
		r.UltraRain = true
		gate := false
		for _, d := range []time.Duration{-time.Hour, 0, time.Hour} {
			if vg == nil {
				break
			}
			if pp := vg.pop(t.Add(d)); pp >= e.o.Rules.SoonPopMin {
				gate = true
			}
			if vp := vg.pty(t.Add(d)); vp > 0 {
				gate = true
			}
		}
		if gate {
			if len(r.Candidates) == 0 {
				r.StartPty = p
			}
			r.Candidates = append(r.Candidates, t)
			if vg != nil {
				r.PopMax = max(r.PopMax, vg.pop(t))
			}
		}
	}
	// 초단기 비 예보의 끝 (첫 후보부터 이어진 PTY 1~4)
	if len(r.Candidates) > 0 {
		r.RainTo = r.Candidates[0]
		for t := r.Candidates[0].Add(time.Hour); ; t = t.Add(time.Hour) {
			if p := ug.pty(t); p < 1 || p > 4 {
				break
			}
			r.RainTo = t
		}
	}
	nowHour := now.Truncate(time.Hour)
	obs, err := e.o.DB.ObsRange(ctx, nx, ny, kst.ToInt(nowHour.Add(-time.Duration(e.o.Rules.DryWindow-1)*time.Hour)), kst.ToInt(nowHour))
	if err != nil {
		return r, err
	}
	for _, m := range obs {
		if p, ok := atoi(m["PTY"]); ok && p != 0 {
			r.ObsRain = true
		}
		if v, err := strconv.ParseFloat(m["RN1"], 64); err == nil && v > 0 {
			r.ObsRain = true
		}
	}
	return r, nil
}

// CheckSoon은 (nx, ny)의 '곧 비'를 판정하고 조건을 만족하면 보낸다.
func (e *Engine) CheckSoon(ctx context.Context, nx, ny int, base int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	favs, err := e.favoritesAt(ctx, nx, ny, store.AlertSoon)
	if err != nil || len(favs) == 0 {
		return err
	}
	now := e.o.Now()
	r, err := e.JudgeSoon(ctx, nx, ny, base, now)
	if err != nil {
		return err
	}
	for _, f := range favs {
		if err := e.updateEpisodes(ctx, f, r.UltraRain || r.ObsRain, now); err != nil {
			return err
		}
		if len(r.Candidates) == 0 || r.ObsRain {
			continue
		}
		last, err := e.o.DB.LastAlertJudged(ctx, f.ID, store.AlertSoon)
		if err != nil {
			return err
		}
		if last > 0 && now.Sub(time.Unix(last, 0)) < e.o.Rules.Cooldown {
			e.o.Log.Info("alert: soon in cooldown", "fav", f.ID, "start", r.Candidates[0].Format("15:04"))
			continue
		}
		start := r.Candidates[0]
		body := fmt.Sprintf("1~3시간 안에 %s 소식 (%d시 전후)", precipWord(r.StartPty), start.Hour())
		key := kst.ToInt(start)
		ins, err := e.o.DB.InsertAlertEvent(ctx, store.AlertEvent{
			FavoriteID: f.ID, Kind: store.AlertSoon, Key: key, NX: nx, NY: ny, JudgedAt: now.Unix(),
			RainFrom: key, RainTo: kst.ToInt(r.RainTo), PopMax: r.PopMax, Source: "ultra:" + strconv.FormatInt(base, 10),
			LastRainSeen: now.Unix(), Body: body,
		})
		if err != nil {
			return err
		}
		if !ins {
			continue
		}
		e.send(ctx, f, store.AlertSoon, key, push.Message{
			Title: f.Name, Body: body, URL: fmt.Sprintf("/?fav=%d", f.ID), Tag: fmt.Sprintf("soon-%d", f.ID),
			TTL: e.o.Rules.SoonTTL, Urgency: "high",
		})
	}
	return nil
}

func (e *Engine) updateEpisodes(ctx context.Context, f store.Favorite, rain bool, now time.Time) error {
	eps, err := e.o.DB.OpenEpisodes(ctx, f.ID)
	if err != nil {
		return err
	}
	for _, ep := range eps {
		switch {
		case rain:
			err = e.o.DB.UpdateEpisode(ctx, f.ID, ep.Key, now.Unix(), 0)
		case now.Sub(time.Unix(ep.LastRainSeen, 0)) > e.o.Rules.EpisodeClose:
			err = e.o.DB.UpdateEpisode(ctx, f.ID, ep.Key, 0, now.Unix())
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// --- 전날 밤 예고 ---

// EveResult는 내일 비 예보 요약이다.
type EveResult struct {
	Base     int64
	From, To time.Time // 해당 시각의 첫·마지막 (To는 마지막 시각 자체)
	PopMax   int
	Word     string
}

// JudgeEve는 day(오늘)의 20시 발표분(없으면 17시)으로 내일의 비 구간을 찾는다. 비가 없거나 발표분이 없으면 ok=false.
func (e *Engine) JudgeEve(ctx context.Context, nx, ny int, day time.Time) (EveResult, bool, error) {
	d0 := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, kst.Loc)
	tomorrow := d0.AddDate(0, 0, 1)
	var g grid
	var base int64
	for _, h := range []int{20, 17} {
		b := kst.ToInt(d0.Add(time.Duration(h) * time.Hour))
		var err error
		if g, err = loadBase(ctx, e.o.DB, "vilage", nx, ny, b, kst.ToInt(tomorrow)); err != nil {
			return EveResult{}, false, err
		}
		if g != nil {
			base = b
			break
		}
	}
	if g == nil {
		return EveResult{}, false, nil
	}
	r := EveResult{Base: base, PopMax: -1}
	rain, snow, mixed := false, false, false
	for h := 0; h < 24; h++ {
		t := tomorrow.Add(time.Duration(h) * time.Hour)
		p, pop := g.pty(t), g.pop(t)
		if p <= 0 && pop < e.o.Rules.EvePopMin {
			continue
		}
		if r.From.IsZero() {
			r.From = t
		}
		r.To = t
		r.PopMax = max(r.PopMax, pop)
		switch p {
		case 2:
			mixed = true
		case 3:
			snow = true
		case 1, 4:
			rain = true
		}
	}
	if r.From.IsZero() {
		return r, false, nil
	}
	switch {
	case mixed || (snow && rain):
		r.Word = "비/눈"
	case snow:
		r.Word = "눈"
	default:
		r.Word = "비"
	}
	return r, true, nil
}

func eveBody(r EveResult) string {
	s := ""
	if r.From.Equal(r.To) {
		s = fmt.Sprintf("내일 %d시 %s", r.From.Hour(), r.Word)
	} else {
		s = fmt.Sprintf("내일 %d~%d시 %s", r.From.Hour(), r.To.Hour()+1, r.Word)
	}
	if r.PopMax >= 0 {
		s += fmt.Sprintf(", 최대 %d%%", r.PopMax)
	}
	return s
}

// CheckEve는 now가 속한 날의 '전날 밤 예고'를 '전날 밤'이 켜진 즐겨찾기마다 (아직 안 했으면) 판정·발송한다.
func (e *Engine) CheckEve(ctx context.Context, now time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	favs, err := e.favoritesAt(ctx, 0, 0, store.AlertEve)
	if err != nil {
		return err
	}
	key := kst.Day(now.AddDate(0, 0, 1))
	for _, f := range favs {
		done, err := e.o.DB.HasAlertEvent(ctx, f.ID, store.AlertEve, key)
		if err != nil {
			return err
		}
		if done {
			continue
		}
		r, ok, err := e.JudgeEve(ctx, f.NX, f.NY, now)
		if err != nil {
			return err
		}
		if !ok {
			if r.Base == 0 {
				e.o.Log.Warn("alert: eve skipped, no 20/17 base", "fav", f.ID, "nx", f.NX, "ny", f.NY)
			}
			continue
		}
		body := eveBody(r)
		ins, err := e.o.DB.InsertAlertEvent(ctx, store.AlertEvent{
			FavoriteID: f.ID, Kind: store.AlertEve, Key: key, NX: f.NX, NY: f.NY, JudgedAt: e.o.Now().Unix(),
			RainFrom: kst.ToInt(r.From), RainTo: kst.ToInt(r.To), PopMax: max(r.PopMax, 0),
			Source: "vilage:" + strconv.FormatInt(r.Base, 10), Body: body,
		})
		if err != nil {
			return err
		}
		if !ins {
			continue
		}
		e.send(ctx, f, store.AlertEve, key, push.Message{
			Title: f.Name, Body: body, URL: fmt.Sprintf("/?fav=%d", f.ID), Tag: fmt.Sprintf("eve-%d", f.ID),
			TTL: e.o.Rules.EveTTL, Urgency: "high",
		})
	}
	return nil
}
