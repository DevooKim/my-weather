package timeline

import (
	"fmt"
	"math"
	"time"

	"myweather/internal/astro"
	"myweather/internal/kma/value"
	"myweather/internal/kst"
)

// 요약 사건 기준 (설계 §3.6 첫 값, Q16)
const (
	EventPopMin     = 60
	EventTempDelta  = 8.0
	EventPrecipSpan = 24 * time.Hour
	EventTempSpan   = 24 * time.Hour
	EventZeroSpan   = 48 * time.Hour
	MaxEvents       = 2 // 강수 시작·그침 한 쌍은 사건 하나로 센다
)

// Summary는 요약 재료와 기본 문장이다(설계 §3.6).
type Summary struct {
	Now       *NowSum  `json:"now"`
	Yesterday *YSum    `json:"yesterday_same_hour"`
	DiffTmp   *float64 `json:"diff_tmp"`
	Events    []Event  `json:"events"`
	Text      []string `json:"text"`
}

// NowSum은 지금 칸 값이다.
type NowSum struct {
	T   time.Time `json:"t"`
	Tmp *float64  `json:"tmp"`
	Pty *string   `json:"pty"`
	Wet bool      `json:"wet"`
	Sky *string   `json:"sky"`
	Src string    `json:"src,omitempty"`
}

// YSum은 어제 같은 시각 값이다. 실황이 없으면 단기예보 추정(estimated).
type YSum struct {
	T         time.Time `json:"t"`
	Tmp       *float64  `json:"tmp"`
	Src       string    `json:"src,omitempty"`
	Estimated bool      `json:"estimated"`
}

// Event는 앞으로의 큰 변화다. type: precip_start | precip_end | below_zero | temp_drop | temp_rise
type Event struct {
	Type  string     `json:"type"`
	At    *time.Time `json:"at,omitempty"`
	Pty   *string    `json:"pty,omitempty"`
	Pop   *int       `json:"pop,omitempty"`
	Src   string     `json:"src,omitempty"`
	Tmp   *float64   `json:"tmp,omitempty"`
	FromT *time.Time `json:"from_t,omitempty"`
	ToT   *time.Time `json:"to_t,omitempty"`
	From  *float64   `json:"from,omitempty"`
	To    *float64   `json:"to,omitempty"`
	Delta *float64   `json:"delta,omitempty"`
}

func tp(t time.Time) *time.Time { return &t }
func fv(v float64) *float64     { return &v }

// precipLike: 요약에서 '강수'로 볼 시각. 비·눈(wet)이거나 강수확률 ≥ 60.
// 초단기 강수형태 5·6·7은 wet이 아니므로 그것만으로는 강수가 아니다(Q1).
func precipLike(h *hour) bool {
	return h.wet() || (h.pop != nil && *h.pop >= EventPopMin)
}

func ptyWord(name string) (word, subj string) {
	switch name {
	case "snow", "flurry":
		return "눈", "눈이"
	case "rain_snow", "drizzle_snow":
		return "비나 눈", "비나 눈이"
	case "shower":
		return "소나기", "소나기가"
	}
	return "비", "비가"
}

func hourLabel(t, now time.Time) string {
	d0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, kst.Loc)
	days := int(t.Sub(d0).Hours()) / 24
	hh := fmt.Sprintf("%02d시", t.Hour())
	switch days {
	case 0:
		return hh
	case 1:
		return "내일 " + hh
	case 2:
		return "모레 " + hh
	}
	return fmt.Sprintf("%d일 %s", t.Day(), hh)
}

func summarize(order []*hour, get func(time.Time) *hour, now, nowHour time.Time) Summary {
	s := Summary{Events: []Event{}, Text: []string{}}
	nh := get(nowHour)
	if nh == nil {
		return s
	}
	ns := &NowSum{T: nowHour, Tmp: nh.tmp, Wet: nh.wet(), Src: nh.obsSrc}
	if n, ok := ptyNames[nh.pty]; ok && nh.pty >= 0 {
		ns.Pty = strp(n)
	}
	if n, ok := skyNames[nh.sky]; ok {
		ns.Sky = strp(n)
	}
	s.Now = ns
	if yh := get(nowHour.Add(-24 * time.Hour)); yh != nil && yh.tmp != nil {
		s.Yesterday = &YSum{T: yh.t, Tmp: yh.tmp, Src: yh.obsSrc, Estimated: yh.estimated}
		if nh.tmp != nil {
			s.DiffTmp = fv(r1(*nh.tmp - *yh.tmp))
		}
	}

	// 첫 문장: 지금 기온과 어제 비교
	if nh.tmp != nil {
		line := fmt.Sprintf("지금 %d℃", int(math.Round(*nh.tmp)))
		if s.DiffTmp != nil {
			y := "어제 이 시간"
			if s.Yesterday.Estimated {
				y += "(추정)"
			}
			n := int(math.Round(math.Abs(*s.DiffTmp)))
			switch {
			case n == 0:
				line += ", " + y + "과 비슷해요"
			case *s.DiffTmp < 0:
				line += fmt.Sprintf(", %s보다 %d도 낮아요", y, n)
			default:
				line += fmt.Sprintf(", %s보다 %d도 높아요", y, n)
			}
		}
		s.Text = append(s.Text, line)
	}

	var future []*hour
	for _, h := range order {
		if h.t.After(nowHour) && !h.t.After(nowHour.Add(EventZeroSpan)) {
			future = append(future, h)
		}
	}
	within := func(h *hour, d time.Duration) bool { return !h.t.After(nowHour.Add(d)) }

	type group struct {
		events []Event
		text   string
	}
	var groups []group

	// 강수
	{
		var g group
		if precipLike(nh) {
			_, subj := ptyWord(deref(ns.Pty))
			for _, h := range future {
				if !within(h, EventPrecipSpan) {
					break
				}
				if !precipLike(h) && h.hasObs() {
					g.events = append(g.events, Event{Type: "precip_end", At: tp(h.t)})
					g.text = fmt.Sprintf("%s쯤 %s 그쳐요", hourLabel(h.t, now), subj)
					break
				}
			}
		} else {
			var startH *hour
			for _, h := range future {
				if !within(h, EventPrecipSpan) {
					break
				}
				if startH == nil && precipLike(h) {
					startH = h
					name := "rain"
					if h.wet() {
						name = ptyNames[h.pty]
					}
					g.events = append(g.events, Event{Type: "precip_start", At: tp(h.t), Pty: strp(name), Pop: h.pop, Src: h.pcpSrc})
					_, subj := ptyWord(name)
					g.text = fmt.Sprintf("%s쯤 %s 시작해요", hourLabel(h.t, now), subj)
					continue
				}
				if startH != nil && !precipLike(h) && h.hasObs() {
					g.events = append(g.events, Event{Type: "precip_end", At: tp(h.t)})
					name := deref(g.events[0].Pty)
					_, subj := ptyWord(name)
					g.text = fmt.Sprintf("%s쯤 %s 시작해 %s쯤 그쳐요", hourLabel(startH.t, now), subj, hourLabel(h.t, now))
					break
				}
			}
		}
		if len(g.events) > 0 {
			groups = append(groups, g)
		}
	}
	// 영하
	if nh.tmp != nil && *nh.tmp >= 0 {
		for _, h := range future {
			if h.tmp != nil && *h.tmp < 0 {
				groups = append(groups, group{
					events: []Event{{Type: "below_zero", At: tp(h.t), Tmp: h.tmp}},
					text:   fmt.Sprintf("%s쯤 영하로 내려가요 (%s℃)", hourLabel(h.t, now), num(*h.tmp)),
				})
				break
			}
		}
	}
	// 기온 변화
	if nh.tmp != nil {
		for _, h := range future {
			if !within(h, EventTempSpan) {
				break
			}
			if h.tmp == nil {
				continue
			}
			d := *h.tmp - *nh.tmp
			if math.Abs(d) >= EventTempDelta {
				typ, verb := "temp_drop", "내려가요"
				if d > 0 {
					typ, verb = "temp_rise", "올라가요"
				}
				groups = append(groups, group{
					events: []Event{{Type: typ, FromT: tp(nowHour), ToT: tp(h.t), From: nh.tmp, To: h.tmp, Delta: fv(r1(d))}},
					text:   fmt.Sprintf("%s까지 %d도 %s", hourLabel(h.t, now), int(math.Round(math.Abs(d))), verb),
				})
				break
			}
		}
	}
	if len(groups) > MaxEvents {
		groups = groups[:MaxEvents]
	}
	for _, g := range groups {
		s.Events = append(s.Events, g.events...)
		s.Text = append(s.Text, g.text)
	}
	return s
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// --- 일별 요약 (§3.7) ---

// Day는 하루 한 줄이다.
type Day struct {
	Date      string   `json:"date"`
	Label     string   `json:"label"`
	Tmin      *float64 `json:"tmin"`
	Tmax      *float64 `json:"tmax"`
	TminSrc   string   `json:"tmin_src,omitempty"`
	TmaxSrc   string   `json:"tmax_src,omitempty"`
	Sky       *string  `json:"sky"`
	Pty       string   `json:"pty"`
	Wet       bool     `json:"wet"`
	PopMax    *int     `json:"pop_max"`
	Pcp       *Amount  `json:"pcp"`
	Estimated bool     `json:"estimated"`
	Complete  bool     `json:"complete"`
	Sun       *Sun     `json:"sun"`
	MoonPhase float64  `json:"moon_phase"`
}

// Sun은 일출·일몰이다.
type Sun struct {
	Rise time.Time `json:"rise"`
	Set  time.Time `json:"set"`
}

var dayLabels = map[int]string{-1: "어제", 0: "오늘", 1: "내일", 2: "모레", 3: "글피", 4: "그글피"}

// skyCloud: 하늘상태 흐린 정도 순위 (맑음 1 < 구름많음 3 < 흐림 4)
func skyCloud(c int) int {
	switch c {
	case 1:
		return 1
	case 3:
		return 2
	case 4:
		return 3
	}
	return 0
}

func daily(order []*hour, today time.Time, place Place) []Day {
	var out []Day
	for i := -1; i <= 4; i++ {
		d := today.AddDate(0, 0, i)
		next := d.AddDate(0, 0, 1)
		var hs []*hour
		for _, h := range order {
			if !h.t.Before(d) && h.t.Before(next) {
				hs = append(hs, h)
			}
		}
		withData := 0
		ext := false
		for _, h := range hs {
			if h.hasObs() {
				withData++
			}
			if h.ext {
				ext = true
			}
		}
		if i == 4 && withData <= 1 {
			break // D+4는 데이터가 있을 때만 (02~14시 발표의 끝점 D+4 00시 하나뿐이면 넣지 않음)
		}
		day := Day{Date: d.Format("2006-01-02"), Label: dayLabels[i], Pty: "none"}
		day.Complete = withData == 24 && !ext && i < 4
		day.MoonPhase = astro.MoonPhase(d.Add(12 * time.Hour))
		if rise, set, ok := astro.SunTimes(d, place.Lat, place.Lon, kst.Loc); ok {
			day.Sun = &Sun{Rise: rise, Set: set}
		}

		// 시간별 최저·최고
		for _, h := range hs {
			if h.tmp == nil {
				continue
			}
			if day.Tmin == nil || *h.tmp < *day.Tmin {
				day.Tmin, day.TminSrc = fv(*h.tmp), h.obsSrc
			}
			if day.Tmax == nil || *h.tmp > *day.Tmax {
				day.Tmax, day.TmaxSrc = fv(*h.tmp), h.obsSrc
			}
			if h.estimated {
				day.Estimated = true
			}
		}
		// 오늘·미래: 단기예보 TMN·TMX. 미래는 TMN·TMX가 우선, 오늘은 시간값과 합쳐 최저·최고.
		if i >= 0 {
			for _, h := range hs {
				if h.tmn != nil && (i > 0 || day.Tmin == nil || *h.tmn < *day.Tmin) {
					day.Tmin, day.TminSrc = fv(*h.tmn), h.skySrc
				}
				if h.tmx != nil && (i > 0 || day.Tmax == nil || *h.tmx > *day.Tmax) {
					day.Tmax, day.TmaxSrc = fv(*h.tmx), h.skySrc
				}
			}
		}
		// 하늘: 09~18시 중 가장 흐린 값
		best := 0
		for _, h := range hs {
			if hh := h.t.Hour(); hh >= 9 && hh <= 18 && skyCloud(h.sky) > skyCloud(best) {
				best = h.sky
			}
		}
		if n, ok := skyNames[best]; ok {
			day.Sky = strp(n)
		}
		// 강수형태: 그날 비·눈으로 볼 시각 중 가장 센 것 (초단기 5·6·7만 있으면 none)
		if p, wet := strongest(hs); wet && p != nil {
			day.Pty, day.Wet = *p, true
		}
		var pcps []*value.Amount
		for _, h := range hs {
			if h.pop != nil && (day.PopMax == nil || *h.pop > *day.PopMax) {
				v := *h.pop
				day.PopMax = &v
			}
			if h.pcp != nil {
				pcps = append(pcps, h.pcp)
			}
		}
		day.Pcp = amount(sum(pcps), false)
		out = append(out, day)
	}
	return out
}
