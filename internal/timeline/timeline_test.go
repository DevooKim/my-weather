package timeline

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"myweather/internal/kst"
	"myweather/internal/store"
)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, kst.Loc)
	if err != nil {
		panic(err)
	}
	return t
}

// valFn은 (예보 시각, 카테고리) → 값. 빈 문자열이면 기본값.
type valFn func(t time.Time, cat string) string

func defVal(t time.Time, cat string, ext bool) string {
	switch cat {
	case "TMP", "T1H":
		return fmt.Sprintf("%d", 5+t.Hour()%12)
	case "SKY":
		return "3"
	case "POP":
		return "20"
	case "PTY":
		return "0"
	case "PCP":
		if ext {
			return "0"
		}
		return "강수없음"
	case "RN1":
		return "강수없음"
	case "SNO":
		if ext {
			return "0"
		}
		return "적설없음"
	case "REH":
		return "60"
	case "WSD":
		if ext {
			return "1"
		}
		return "2.0"
	case "VEC":
		return "270"
	case "UUU":
		return "2.0"
	case "VVV":
		return "0.0"
	}
	return "0"
}

// vilage는 실제와 같은 시각 배치로 단기 발표분을 만든다.
// 02~14시 발표: 1시간 간격 D+3 00시까지, 이후 3시간 간격 D+4 00시까지.
// 17~23시 발표: 1시간 간격 D+4 00시까지, 이후 3시간 간격 D+5 00시까지.
func vilage(base time.Time, f valFn) Fcst {
	d0 := time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, kst.Loc)
	hourlyEnd, end := d0.AddDate(0, 0, 3), d0.AddDate(0, 0, 4)
	if base.Hour() >= 17 {
		hourlyEnd, end = d0.AddDate(0, 0, 4), d0.AddDate(0, 0, 5)
	}
	var rows []store.Row
	add := func(t time.Time, ext bool) {
		cats := []string{"TMP", "UUU", "VVV", "VEC", "WSD", "SKY", "PTY", "POP", "PCP", "REH", "SNO"}
		if t.Hour() == 6 {
			cats = append(cats, "TMN")
		}
		if t.Hour() == 15 {
			cats = append(cats, "TMX")
		}
		for _, c := range cats {
			v := ""
			if f != nil {
				v = f(t, c)
			}
			if v == "" {
				switch c {
				case "TMN":
					v = "1.0"
				case "TMX":
					v = "20.0"
				default:
					v = defVal(t, c, ext)
				}
			}
			rows = append(rows, store.Row{At: kst.ToInt(t), Category: c, Value: v})
		}
	}
	t := base.Add(time.Hour)
	for ; !t.After(hourlyEnd); t = t.Add(time.Hour) {
		add(t, false)
	}
	for t = hourlyEnd.Add(3 * time.Hour); !t.After(end); t = t.Add(3 * time.Hour) {
		add(t, true)
	}
	return Fcst{Base: base, Ver: 1, Rows: rows}
}

func ultra(base time.Time, f valFn) Fcst {
	var rows []store.Row
	start := base.Truncate(time.Hour).Add(time.Hour)
	for h := 0; h < 6; h++ {
		t := start.Add(time.Duration(h) * time.Hour)
		for _, c := range []string{"T1H", "RN1", "SKY", "UUU", "VVV", "REH", "PTY", "LGT", "VEC", "WSD"} {
			v := ""
			if f != nil {
				v = f(t, c)
			}
			if v == "" {
				v = defVal(t, c, false)
			}
			rows = append(rows, store.Row{At: kst.ToInt(t), Category: c, Value: v})
		}
	}
	return Fcst{Base: base, Ver: 1, Rows: rows}
}

func obsAt(t time.Time, tmp float64, pty string) map[string]string {
	return map[string]string{"T1H": fmt.Sprintf("%.1f", tmp), "RN1": "0", "UUU": "1", "VVV": "1", "REH": "50", "PTY": pty, "VEC": "225", "WSD": "1.4"}
}

// favInput: 수집 대상 격자처럼 모든 발표분과 실황이 있는 입력
func favInput(now time.Time) Input {
	in := Input{Now: now, Place: Place{NX: 59, NY: 126, Lat: 37.4, Lon: 127.1, Name: "서교동"}, Obs: map[int64]map[string]string{}}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, kst.Loc)
	latest := kst.Vilage.Latest(now)
	for b := today.AddDate(0, 0, -2).Add(23 * time.Hour); !b.After(latest); b = b.Add(3 * time.Hour) {
		in.Vilage = append(in.Vilage, vilage(b, nil))
	}
	in.Ultra = []Fcst{ultra(kst.Ultra.Latest(now), nil)}
	for t := today.AddDate(0, 0, -1); !t.After(kst.Ncst.Latest(now)); t = t.Add(time.Hour) {
		in.Obs[kst.ToInt(t)] = obsAt(t, 3, "0")
	}
	return in
}

func slotAt(t *testing.T, tl *Timeline, s string) Slot {
	t.Helper()
	w := at(s)
	for _, sl := range tl.Slots {
		if sl.T.Equal(w) {
			return sl
		}
	}
	t.Fatalf("no slot at %s", s)
	return Slot{}
}

func TestSlotLayout1420(t *testing.T) {
	now := at("2026-11-10 14:20")
	tl := Build(favInput(now))
	if !tl.Range.Start.Equal(at("2026-11-09 00:00")) || !tl.Range.HourlyFrom.Equal(at("2026-11-10 12:00")) {
		t.Fatalf("range %+v", tl.Range)
	}
	// 최신 단기 = 14시 발표 → D+4 00시까지
	if !tl.Range.End.Equal(at("2026-11-14 00:00")) {
		t.Fatalf("end %s", tl.Range.End)
	}
	var past3, hourly, fut3 int
	for i, s := range tl.Slots {
		if i > 0 && !s.T.After(tl.Slots[i-1].T) {
			t.Fatalf("slots not increasing at %d", i)
		}
		switch {
		case s.H == 3 && s.Phase == "past":
			past3++
		case s.H == 1:
			hourly++
		case s.H == 3 && s.Phase == "future":
			fut3++
		}
	}
	// 어제 00~21 (8) + 오늘 00·03·06·09 (4) / 오늘 12~23 (12) / 11-11 00 ~ 11-14 00 (25)
	if past3 != 12 || hourly != 12 || fut3 != 25 {
		t.Fatalf("past3=%d hourly=%d fut3=%d", past3, hourly, fut3)
	}
	if s := slotAt(t, tl, "2026-11-10 13:00"); s.Phase != "past" || s.Src.Obs != "n" {
		t.Fatalf("13h %+v", s)
	}
	if s := slotAt(t, tl, "2026-11-10 14:00"); s.Phase != "now" || s.Src.Obs != "n" {
		t.Fatalf("now %+v", s)
	}
	if s := slotAt(t, tl, "2026-11-10 15:00"); s.Phase != "future" {
		t.Fatalf("15h %+v", s)
	}
}

func TestSlotLayoutEdges(t *testing.T) {
	cases := []struct {
		now       string
		hourlyN   int
		firstHour string
	}{
		{"2026-11-10 00:05", 24, "2026-11-10 00:00"},
		{"2026-11-10 00:20", 24, "2026-11-10 00:00"},
		{"2026-11-10 21:40", 3, "2026-11-10 21:00"},
		{"2026-11-10 22:20", 3, "2026-11-10 21:00"},
		{"2026-11-10 23:59", 3, "2026-11-10 21:00"},
	}
	for _, c := range cases {
		tl := Build(favInput(at(c.now)))
		n := 0
		var first time.Time
		for _, s := range tl.Slots {
			if s.H == 1 {
				if n == 0 {
					first = s.T
				}
				n++
			}
		}
		if n != c.hourlyN || !first.Equal(at(c.firstHour)) {
			t.Errorf("%s: hourly=%d first=%s", c.now, n, first.Format("15:04"))
		}
		// 3시간 과거 칸이 지금 블록과 겹치지 않음
		for _, s := range tl.Slots {
			if s.H == 3 && s.Phase == "past" && s.T.Add(3*time.Hour).After(at(c.firstHour)) {
				t.Errorf("%s: past 3h slot %s overlaps hourly block", c.now, s.T.Format("01-02 15"))
			}
		}
	}
	// 00:05: 00시 실황이 아직 없으면 23시 실황을 지금 칸에 씀
	in := favInput(at("2026-11-10 00:05"))
	delete(in.Obs, kst.ToInt(at("2026-11-10 00:00")))
	tl := Build(in)
	if s := slotAt(t, tl, "2026-11-10 00:00"); s.Src.Obs != "n" || s.Tmp == nil {
		t.Fatalf("00:05 now slot %+v", s)
	}
}

func TestSourcesUltraPrecipCutoff(t *testing.T) {
	now := at("2026-11-10 14:50") // 초단기 14:30 발표 → 15~20시
	in := favInput(now)
	in.Ultra = []Fcst{ultra(at("2026-11-10 14:30"), func(t time.Time, c string) string {
		if c == "PTY" && t.Hour() == 18 {
			return "1" // +3.5h: 단기로 넘어가야 하므로 무시돼야 함
		}
		if c == "T1H" {
			return "30"
		}
		return ""
	})}
	tl := Build(in)
	for _, h := range []string{"15:00", "16:00", "17:00"} {
		s := slotAt(t, tl, "2026-11-10 "+h)
		if s.Src.Obs != "u1" || s.Src.Pcp != "u1" || *s.Tmp != 30 {
			t.Fatalf("%s: %+v", h, s.Src)
		}
	}
	s := slotAt(t, tl, "2026-11-10 18:00")
	if s.Src.Obs != "u1" || s.Src.Pcp != "v1" || s.Wet || *s.Pty != "none" {
		t.Fatalf("18h: src %+v pty %v wet %v", s.Src, *s.Pty, s.Wet)
	}
	if s := slotAt(t, tl, "2026-11-10 21:00"); s.Src.Obs != "v1" || *s.Tmp == 30 {
		t.Fatalf("21h: %+v", s.Src)
	}
	if s := slotAt(t, tl, "2026-11-10 15:00"); s.Src.Sky != "v1" {
		t.Fatalf("sky src %+v", s.Src)
	}
	if tl.Sources["u1"].Kind != KindUltra || tl.Sources["v1"].Delayed != nil || tl.Sources["n"].Kind != KindNcst {
		t.Fatalf("sources %+v", tl.Sources)
	}
}

func TestUltraDrizzleNotRain(t *testing.T) {
	now := at("2026-11-10 14:50")
	in := favInput(now)
	in.Ultra = []Fcst{ultra(at("2026-11-10 14:30"), func(t time.Time, c string) string {
		if c == "PTY" && t.Hour() == 16 {
			return "5"
		}
		if c == "RN1" && t.Hour() == 16 {
			return "1mm 미만"
		}
		return ""
	})}
	tl := Build(in)
	s := slotAt(t, tl, "2026-11-10 16:00")
	if s.Wet || s.Pty == nil || *s.Pty != "drizzle" {
		t.Fatalf("1h slot: wet=%v pty=%v", s.Wet, s.Pty)
	}
	for _, e := range tl.Summary.Events {
		if strings.HasPrefix(e.Type, "precip") {
			t.Fatalf("drizzle produced event %+v", e)
		}
	}
	for _, d := range tl.Daily {
		if d.Label == "오늘" && (d.Wet || d.Pty != "none") {
			t.Fatalf("daily %+v", d)
		}
	}
	// 3시간 칸 집계: 같은 칸에 단기 비(PTY 1)가 있으면 비, 빗방울만 있으면 wet=false
	g := map[int64]*hour{}
	mk := func(s string, pty int, kind string) *hour {
		h := &hour{t: at(s), pty: pty, ptyKind: kind}
		g[kst.ToInt(h.t)] = h
		return h
	}
	mk("2026-11-11 00:00", 5, KindUltra)
	mk("2026-11-11 01:00", 0, KindVilage)
	mk("2026-11-11 02:00", 0, KindVilage)
	p, wet := strongest([]*hour{g[202611110000], g[202611110100], g[202611110200]})
	if wet || *p != "drizzle" {
		t.Fatalf("agg drizzle: %v %v", *p, wet)
	}
	g[202611110100].pty = 1
	p, wet = strongest([]*hour{g[202611110000], g[202611110100], g[202611110200]})
	if !wet || *p != "rain" {
		t.Fatalf("agg rain: %v %v", *p, wet)
	}
	g[202611110200].pty = 3
	p, _ = strongest([]*hour{g[202611110000], g[202611110100], g[202611110200]})
	if *p != "rain_snow" {
		t.Fatalf("rain+snow → %v", *p)
	}
	// 실황의 빗방울(5)은 실제 관측이므로 wet
	if h := (&hour{pty: 5, ptyKind: KindNcst}); !h.wet() {
		t.Fatal("ncst drizzle should be wet")
	}
}

func TestNonFavoritePastEstimated(t *testing.T) {
	now := at("2026-11-10 14:20")
	// 즉시 조회 격자: 실황은 최근 24시간만, 단기는 그저께 23시·어제 11시·어제 23시·오늘 11시·최신(14시)
	in := Input{Now: now, Place: Place{NX: 60, NY: 127, Lat: 37.5, Lon: 127}, Obs: map[int64]map[string]string{}}
	for _, b := range []string{"2026-11-08 23:00", "2026-11-09 11:00", "2026-11-09 23:00", "2026-11-10 11:00", "2026-11-10 14:00"} {
		in.Vilage = append(in.Vilage, vilage(at(b), nil))
	}
	for t := at("2026-11-09 15:00"); !t.After(at("2026-11-10 14:00")); t = t.Add(time.Hour) {
		in.Obs[kst.ToInt(t)] = obsAt(t, 3, "0")
	}
	tl := Build(in)
	s := slotAt(t, tl, "2026-11-09 03:00")
	if !s.Estimated || s.Src.Obs != tl.keyOf(t, "2026-11-08 23:00") || s.Phase != "past" {
		t.Fatalf("yesterday 03: %+v", s)
	}
	if s := slotAt(t, tl, "2026-11-09 12:00"); !s.Estimated || s.Src.Obs != tl.keyOf(t, "2026-11-09 11:00") {
		t.Fatalf("yesterday 12: %+v", s.Src)
	}
	if s := slotAt(t, tl, "2026-11-09 15:00"); s.Estimated || s.Src.Obs != "n" {
		t.Fatalf("yesterday 15 (실황): %+v", s)
	}
	if y := tl.Summary.Yesterday; y == nil || !y.Estimated {
		t.Fatalf("yesterday same hour should be estimated: %+v", y)
	}
	if !strings.Contains(tl.Summary.Text[0], "(추정)") {
		t.Fatalf("text %q", tl.Summary.Text)
	}
	if d := tl.Daily[0]; d.Label != "어제" || !d.Estimated {
		t.Fatalf("daily yesterday %+v", d)
	}
}

// keyOf는 base 시각에 붙은 sources 키를 찾는다.
func (tl *Timeline) keyOf(t *testing.T, base string) string {
	t.Helper()
	b := at(base)
	for k, s := range tl.Sources {
		if s.Base != nil && s.Base.Equal(b) {
			return k
		}
	}
	t.Fatalf("no source for base %s", base)
	return ""
}

func TestExtendedSegment(t *testing.T) {
	now := at("2026-11-10 14:20") // 14시 발표: 1시간 간격 11-13 00시까지, 3시간 11-13 03 ~ 11-14 00
	in := favInput(now)
	in.Vilage[len(in.Vilage)-1] = vilage(at("2026-11-10 14:00"), func(t time.Time, c string) string {
		if t.Equal(at("2026-11-13 06:00")) {
			switch c {
			case "PCP":
				return "2"
			case "SNO":
				return "1"
			case "WSD":
				return "2"
			case "UUU":
				return "3"
			case "VVV":
				return "4"
			case "PTY":
				return "1"
			}
		}
		return ""
	})
	tl := Build(in)
	s := slotAt(t, tl, "2026-11-13 06:00")
	if s.Res != "3h" || s.H != 3 || s.Pcp == nil || s.Pcp.Code == nil || *s.Pcp.Code != 2 || s.Pcp.Text != "보통 비" {
		t.Fatalf("ext pcp %+v", s.Pcp)
	}
	if s.WsdCode == nil || *s.WsdCode != 2 || s.Wsd == nil || *s.Wsd != 5 || s.Sno.Text != "보통 눈" {
		t.Fatalf("ext wsd %v %v sno %+v", s.WsdCode, s.Wsd, s.Sno)
	}
	// 11-13 00시는 마지막 1시간 값이라 칸 안의 01·02시가 없음 → partial
	s = slotAt(t, tl, "2026-11-13 00:00")
	if s.Res != "1h" || !s.Partial || s.Pcp.Code != nil {
		t.Fatalf("D+3 00 slot %+v", s)
	}
	if s := slotAt(t, tl, "2026-11-12 21:00"); s.Partial || s.Res != "1h" {
		t.Fatalf("hourly 3h slot partial %+v", s)
	}
}

func TestAggregate3h(t *testing.T) {
	now := at("2026-11-10 14:20")
	in := favInput(now)
	in.Vilage[len(in.Vilage)-1] = vilage(at("2026-11-10 14:00"), func(t time.Time, c string) string {
		if t.Day() == 11 && t.Hour() >= 3 && t.Hour() <= 5 {
			switch c {
			case "PCP":
				return map[int]string{3: "1mm 미만", 4: "2.0mm", 5: "30.0~50.0mm"}[t.Hour()]
			case "POP":
				return map[int]string{3: "60", 4: "80", 5: "70"}[t.Hour()]
			case "PTY":
				return map[int]string{3: "1", 4: "3", 5: "1"}[t.Hour()]
			case "TMP":
				return map[int]string{3: "4", 4: "9", 5: "9"}[t.Hour()]
			}
		}
		return ""
	})
	tl := Build(in)
	s := slotAt(t, tl, "2026-11-11 03:00")
	if *s.Tmp != 4 || *s.Pop != 80 || *s.Pty != "rain_snow" || !s.Wet {
		t.Fatalf("agg %v %v %v", *s.Tmp, *s.Pop, *s.Pty)
	}
	if s.Pcp.Min != 32 || *s.Pcp.Max != 53 || s.Pcp.MM != 42.5 || s.Pcp.Exact || s.Pcp.Text != "32~53mm" {
		t.Fatalf("pcp %+v", s.Pcp)
	}
	if s.Partial {
		t.Fatal("not partial")
	}
}

func TestDelayedAndKeys(t *testing.T) {
	now := at("2026-11-10 14:40")
	in := favInput(now)
	in.Vilage = in.Vilage[:len(in.Vilage)-1] // 14시 발표 없음 → 11시 발표 사용
	tl := Build(in)
	v1 := tl.Sources["v1"]
	if v1.Base == nil || !v1.Base.Equal(at("2026-11-10 11:00")) || v1.Delayed == nil || !v1.Delayed.ExpectedBase.Equal(at("2026-11-10 14:00")) {
		t.Fatalf("v1 %+v", v1)
	}
	if !tl.Range.End.Equal(at("2026-11-14 00:00")) || len(tl.Warnings) == 0 || tl.Warnings[0] != "vilage_fallback" {
		t.Fatalf("end %s warnings %v", tl.Range.End, tl.Warnings)
	}
	// 발표 직후(+5분 이내)는 지연으로 보지 않음
	in = favInput(at("2026-11-10 14:12"))
	in.Vilage = in.Vilage[:len(in.Vilage)-1]
	if tl := Build(in); tl.Sources["v1"].Delayed != nil {
		t.Fatal("grace")
	}

	// 수집 대상 격자와 즉시 조회 격자의 응답 키가 같고 archived·fav_id가 없음
	keys := func(tl *Timeline) string {
		b, _ := json.Marshal(tl)
		var m map[string]any
		json.Unmarshal(b, &m)
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		for k := range m["place"].(map[string]any) {
			ks = append(ks, "place."+k)
		}
		return fmt.Sprint(sortStrings(ks))
	}
	fav := Build(favInput(now))
	nonFav := favInput(now)
	nonFav.Obs = map[int64]map[string]string{}
	other := Build(nonFav)
	if keys(fav) != keys(other) {
		t.Fatalf("keys differ:\n%s\n%s", keys(fav), keys(other))
	}
	b, _ := json.Marshal(fav)
	if strings.Contains(string(b), "archived") || strings.Contains(string(b), "fav_id") {
		t.Fatal("archived/fav_id leaked")
	}
}

func sortStrings(s []string) []string {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
	return s
}

func TestSummary(t *testing.T) {
	now := at("2026-11-10 14:20")
	in := favInput(now)
	in.Obs[kst.ToInt(at("2026-11-10 14:00"))] = obsAt(at("2026-11-10 14:00"), 14.8, "0")
	in.Obs[kst.ToInt(at("2026-11-09 14:00"))] = obsAt(at("2026-11-09 14:00"), 17.9, "0")
	in.Vilage[len(in.Vilage)-1] = vilage(at("2026-11-10 14:00"), func(t time.Time, c string) string {
		wet := t.After(at("2026-11-10 16:00")) && t.Before(at("2026-11-10 21:00"))
		switch c {
		case "PTY":
			if wet {
				return "1"
			}
		case "POP":
			if wet {
				return "60"
			}
		case "TMP":
			if t.Equal(at("2026-11-11 06:00")) {
				return "-1"
			}
			return "12"
		}
		return ""
	})
	in.Ultra = nil // 강수는 단기만
	tl := Build(in)
	s := tl.Summary
	if s.DiffTmp == nil || *s.DiffTmp != -3.1 || s.Yesterday.Estimated {
		t.Fatalf("diff %+v", s)
	}
	if len(s.Events) != 3 || s.Events[0].Type != "precip_start" || !s.Events[0].At.Equal(at("2026-11-10 17:00")) ||
		s.Events[1].Type != "precip_end" || !s.Events[1].At.Equal(at("2026-11-10 21:00")) || s.Events[2].Type != "below_zero" {
		t.Fatalf("events %+v", s.Events)
	}
	want := []string{"지금 15℃, 어제 이 시간보다 3도 낮아요", "17시쯤 비가 시작해 21시쯤 그쳐요", "내일 06시쯤 영하로 내려가요 (-1℃)"}
	if fmt.Sprint(s.Text) != fmt.Sprint(want) {
		t.Fatalf("text %q", s.Text)
	}
}

func TestDaily(t *testing.T) {
	now := at("2026-11-10 20:20") // 20시 발표 → D+5 00시까지, D+4(11-14)는 3시간 간격
	tl := Build(favInput(now))
	if len(tl.Daily) != 6 {
		t.Fatalf("days %d", len(tl.Daily))
	}
	labels := []string{"어제", "오늘", "내일", "모레", "글피", "그글피"}
	for i, d := range tl.Daily {
		if d.Label != labels[i] {
			t.Fatalf("label %d %s", i, d.Label)
		}
		if d.Sun == nil || d.Sun.Rise.Hour() != 7 || d.Sun.Set.Hour() != 17 {
			t.Fatalf("sun %+v", d.Sun)
		}
	}
	y, tm, d4 := tl.Daily[0], tl.Daily[2], tl.Daily[5]
	if *y.Tmin != 3 || *y.Tmax != 3 || y.TminSrc != "n" || !y.Complete || y.Estimated {
		t.Fatalf("yesterday %+v", y)
	}
	if *tm.Tmin != 1 || *tm.Tmax != 20 || tm.TminSrc != "v1" || *tm.Sky != "partly" || !tm.Complete {
		t.Fatalf("tomorrow %+v", tm)
	}
	if d4.Complete || d4.Date != "2026-11-14" {
		t.Fatalf("D+4 %+v", d4)
	}
	// 02~14시 발표만 있으면 D+4는 빠짐
	tl = Build(favInput(at("2026-11-10 14:20")))
	if len(tl.Daily) != 5 || tl.Daily[4].Complete {
		t.Fatalf("days %d last %+v", len(tl.Daily), tl.Daily[len(tl.Daily)-1])
	}
}

func TestEmptyInput(t *testing.T) {
	tl := Build(Input{Now: at("2026-11-10 14:20"), Place: Place{NX: 1, NY: 1}})
	if len(tl.Slots) == 0 || tl.Warnings[0] != "no_vilage" || len(tl.Daily) != 5 {
		t.Fatalf("%+v", tl)
	}
	if _, err := json.Marshal(tl); err != nil {
		t.Fatal(err)
	}
}
