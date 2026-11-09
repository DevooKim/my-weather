// Package timeline은 저장된 실황·초단기·단기예보로 타임라인 응답(설계 §3)을 만든다.
//
// 순서:
//  1. 어제 00시부터 예보 끝까지 시각마다 기록(hour) 하나를 만든다. 기록에는 값 묶음 두 개와 각 출처가 있다.
//     - 관측 묶음(기온·강수형태·강수량·바람·습도): 지난 시각은 저장된 실황, 없으면 단기예보(estimated).
//     지금 칸은 최신 실황(정시 실황이 아직 없으면 한 시간 전). 미래는 초단기예보가 덮는 시각이면 초단기,
//     아니면 단기. 단, 강수형태·강수량은 초단기 발표 +2.5시간까지만 초단기이고 그 뒤는 단기다(Q1).
//     - 하늘 묶음(하늘상태·강수확률·적설·TMN·TMX): 늘 단기예보.
//     - 단기예보는 시각 t마다 "t를 담고 있는 저장된 발표분 중 가장 늦은 것"을 쓴다. 지난 칸에서는 이것이 직전 발표,
//     미래 칸에서는 최신 발표분이 된다(설계 §3.2를 한 규칙으로 정리).
//  2. 기록을 칸(1시간·3시간)으로 자르고 3시간 칸은 집계한다(§3.1).
//  3. 요약 재료(§3.6)와 일별 요약(§3.7)을 만든다.
//
// 이 패키지는 격자가 수집 대상(즐겨찾기)인지 모른다. 출처는 저장된 데이터로만 정한다(Q14: archived를 노출하지 않음).
package timeline

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"myweather/internal/kma/value"
	"myweather/internal/kst"
	"myweather/internal/store"
)

// 출처 종류
const (
	KindNcst   = "ncst"
	KindUltra  = "ultra"
	KindVilage = "vilage"
)

// UltraPrecipLead는 초단기 강수형태·강수량을 쓰는 한계(발표 시각 기준)다. hh30 발표면 hh+1·hh+2·hh+3시(Q1).
const UltraPrecipLead = 150 * time.Minute

// DelayGrace는 예정 발표분이 저장되지 않았을 때 '지연'으로 보기까지의 여유다(수집 직후 깜빡임 방지).
const DelayGrace = 5 * time.Minute

// Fcst는 저장된 발표분 하나(가장 높은 ver)다.
type Fcst struct {
	Base time.Time
	Ver  int
	Rows []store.Row
}

// Place는 응답의 장소 정보다. Name·Full·Region은 표에 없는 격자면 빈 값.
type Place struct {
	Name   string  `json:"name,omitempty"`
	Full   string  `json:"full,omitempty"`
	Region string  `json:"region,omitempty"`
	NX     int     `json:"nx"`
	NY     int     `json:"ny"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
}

// Input은 타임라인 재료다. 시각은 모두 KST.
type Input struct {
	Now    time.Time
	Place  Place
	Vilage []Fcst
	Ultra  []Fcst
	Obs    map[int64]map[string]string // obs_at → category → value
}

// Timeline은 응답 본문이다(설계 §3.3). place.archived·fav_id는 넣지 않는다(Q14).
type Timeline struct {
	Place       Place             `json:"place"`
	Now         time.Time         `json:"now"`
	Range       Range             `json:"range"`
	Complete    bool              `json:"complete"`
	RetryAfterS *int              `json:"retry_after_s"`
	DataVersion string            `json:"data_version"`
	Sources     map[string]Source `json:"sources"`
	Slots       []Slot            `json:"slots"`
	Summary     Summary           `json:"summary"`
	Daily       []Day             `json:"daily"`
	Warnings    []string          `json:"warnings"`
}

// Range는 타임라인 범위다.
type Range struct {
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	HourlyFrom time.Time `json:"hourly_from"`
	HourlyTo   time.Time `json:"hourly_to"`
}

// Source는 sources 맵의 값이다.
type Source struct {
	Kind    string     `json:"kind"`
	Label   string     `json:"label"`
	Base    *time.Time `json:"base,omitempty"`
	Delayed *Delayed   `json:"delayed,omitempty"`
}

// Delayed는 예정 발표분이 없어 이전 발표분으로 대신했다는 표시다.
type Delayed struct {
	ExpectedBase time.Time `json:"expected_base"`
}

// Amount는 강수량·적설 응답 형식이다(설계 §3.4).
type Amount struct {
	MM    float64  `json:"mm"`
	Min   float64  `json:"min"`
	Max   *float64 `json:"max"`
	Exact bool     `json:"exact"`
	Text  string   `json:"text"`
	Code  *int     `json:"code,omitempty"`
}

// SlotSrc는 칸의 출처 키다. Pcp는 강수형태·강수량의 출처(초단기 +2.5h 뒤에는 Obs와 다를 수 있음).
type SlotSrc struct {
	Obs string `json:"obs,omitempty"`
	Pcp string `json:"pcp,omitempty"`
	Sky string `json:"sky,omitempty"`
}

// Slot은 칸 하나다.
type Slot struct {
	T     time.Time `json:"t"`
	H     int       `json:"h"`
	Phase string    `json:"phase"`
	Res   string    `json:"res"`
	Tmp   *float64  `json:"tmp"`
	Feels *float64  `json:"feels"`
	Sky   *string   `json:"sky"`
	Pty   *string   `json:"pty"`
	// Wet은 비·눈으로 그릴지다. 초단기 강수형태 5·6·7(빗방울·날림)은 pty 이름은 남기되 wet=false다(Q1).
	Wet       bool     `json:"wet"`
	Pcp       *Amount  `json:"pcp"`
	Pop       *int     `json:"pop"`
	Reh       *float64 `json:"reh"`
	Wsd       *float64 `json:"wsd"`
	WsdCode   *int     `json:"wsd_code,omitempty"`
	Vec       *float64 `json:"vec"`
	Dir16     *string  `json:"dir16"`
	Uuu       *float64 `json:"uuu"`
	Vvv       *float64 `json:"vvv"`
	Sno       *Amount  `json:"sno"`
	Src       SlotSrc  `json:"src"`
	Estimated bool     `json:"estimated"`
	Partial   bool     `json:"partial"`
}

// hour는 시각 하나의 기록이다.
type hour struct {
	t time.Time

	// 관측 묶음
	tmp, reh, wsd, vec, uuu, vvv *float64
	wsdCode                      *int
	pty                          int // -1 = 없음
	ptyKind                      string
	pcp                          *value.Amount
	obsSrc, pcpSrc               string
	estimated                    bool

	// 하늘 묶음 (단기예보)
	sky      int // 0 = 없음
	pop      *int
	sno      *value.Amount
	tmn, tmx *float64
	skySrc   string
	ext      bool // 단기예보 연장 구간(3시간 간격, 코드값)
}

func (h *hour) hasObs() bool { return h.tmp != nil || h.pty >= 0 }

// wet: 비·눈으로 볼 강수인지. 예보는 PTY 1~4만, 실황은 PTY≠0 모두(실제 관측이므로).
func (h *hour) wet() bool {
	if h.pty <= 0 {
		return false
	}
	if h.ptyKind == KindNcst {
		return true
	}
	return h.pty <= 4
}

type fsrc struct {
	key  string
	kind string
	base time.Time
	ver  int
	at   map[int64]map[string]string
	ext  map[int64]bool
	last time.Time
}

func newFsrc(kind string, f Fcst) *fsrc {
	s := &fsrc{kind: kind, base: f.Base.In(kst.Loc), ver: f.Ver, at: map[int64]map[string]string{}, ext: map[int64]bool{}}
	for _, r := range f.Rows {
		m := s.at[r.At]
		if m == nil {
			m = map[string]string{}
			s.at[r.At] = m
		}
		m[r.Category] = r.Value
	}
	var ts []int64
	for t := range s.at {
		ts = append(ts, t)
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	// 연장 구간: 앞 예보 시각과 3시간 이상 떨어진 첫 시각부터 끝까지. 바로 앞(예: D+3 00시)은 마지막 1시간 값이다.
	extFrom := -1
	for i := 1; i < len(ts); i++ {
		if kst.FromInt(ts[i]).Sub(kst.FromInt(ts[i-1])) >= 3*time.Hour {
			extFrom = i
			break
		}
	}
	if kind == KindVilage && extFrom > 0 {
		for _, t := range ts[extFrom:] {
			s.ext[t] = true
		}
	}
	if len(ts) > 0 {
		s.last = kst.FromInt(ts[len(ts)-1])
	}
	return s
}

func (s *fsrc) get(t time.Time) map[string]string { return s.at[kst.ToInt(t)] }

func pf(m map[string]string, k string) *float64 {
	v, ok := m[k]
	if !ok {
		return nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f <= -900 || f >= 900 { // 결측값(-999 등) 제외
		return nil
	}
	return &f
}

func pi(m map[string]string, k string) *int {
	f := pf(m, k)
	if f == nil {
		return nil
	}
	n := int(math.Round(*f))
	return &n
}

// Build는 타임라인을 만든다. Complete·RetryAfterS·Warnings 일부는 호출자(즉시 조회)가 채운다.
func Build(in Input) *Timeline {
	now := in.Now.In(kst.Loc)
	nowHour := now.Truncate(time.Hour)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, kst.Loc)
	start := today.AddDate(0, 0, -1)
	todayLast := today.Add(23 * time.Hour)

	// 출처 준비. 단기는 base 내림차순으로 v1, vp1, vp2, ...
	var vil []*fsrc
	for _, f := range in.Vilage {
		if len(f.Rows) > 0 {
			vil = append(vil, newFsrc(KindVilage, f))
		}
	}
	sort.Slice(vil, func(i, j int) bool { return vil[i].base.After(vil[j].base) })
	for i, s := range vil {
		if i == 0 {
			s.key = "v1"
		} else {
			s.key = fmt.Sprintf("vp%d", i)
		}
	}
	var ult *fsrc
	for _, f := range in.Ultra {
		if len(f.Rows) == 0 {
			continue
		}
		if ult == nil || f.Base.After(ult.base) {
			ult = newFsrc(KindUltra, f)
		}
	}
	if ult != nil {
		ult.key = "u1"
	}

	end := todayLast
	if len(vil) > 0 && vil[0].last.After(end) {
		end = vil[0].last
	}

	vilageAt := func(t time.Time) (*fsrc, map[string]string) {
		for _, s := range vil {
			if m := s.get(t); m != nil {
				return s, m
			}
		}
		return nil, nil
	}

	used := map[string]bool{}
	hours := map[int64]*hour{}
	var order []*hour
	for t := start; !t.After(end); t = t.Add(time.Hour) {
		h := &hour{t: t, pty: -1}
		vs, vm := vilageAt(t)
		if vs != nil {
			h.ext = vs.ext[kst.ToInt(t)]
			h.skySrc = vs.key
			if c := pi(vm, "SKY"); c != nil {
				h.sky = *c
			}
			h.pop = pi(vm, "POP")
			if v, ok := vm["SNO"]; ok {
				if a, err := value.ParseSNO(v, h.ext); err == nil {
					h.sno = &a
				}
			}
			h.tmn, h.tmx = pf(vm, "TMN"), pf(vm, "TMX")
			used[vs.key] = true
		}
		switch {
		case t.Before(nowHour):
			if m := in.Obs[kst.ToInt(t)]; len(m) > 0 {
				h.fillNcst(m)
			} else if vs != nil {
				h.fillVilage(vs, vm, true)
			}
		case t.Equal(nowHour):
			if m := in.Obs[kst.ToInt(t)]; len(m) > 0 {
				h.fillNcst(m)
			} else if m := in.Obs[kst.ToInt(t.Add(-time.Hour))]; len(m) > 0 {
				h.fillNcst(m) // 정시 실황이 아직 없으면(hh:10 전) 한 시간 전 실황
			} else if um := ultraAt(ult, t); um != nil {
				h.fillUltra(ult, um, vs, vm)
			} else if vs != nil {
				h.fillVilage(vs, vm, false)
			}
		default:
			if um := ultraAt(ult, t); um != nil {
				h.fillUltra(ult, um, vs, vm)
			} else if vs != nil {
				h.fillVilage(vs, vm, false)
			}
		}
		for _, k := range []string{h.obsSrc, h.pcpSrc} {
			if k != "" {
				used[k] = true
			}
		}
		hours[kst.ToInt(t)] = h
		order = append(order, h)
	}

	tl := &Timeline{
		Place:    in.Place,
		Now:      now,
		Range:    Range{Start: start, End: end, HourlyTo: today.AddDate(0, 0, 1)},
		Complete: true,
		Sources:  map[string]Source{},
		Warnings: []string{},
	}
	blockStart := nowHour.Add(-time.Duration(nowHour.Hour()%3) * time.Hour)
	tl.Range.HourlyFrom = blockStart

	// 출처 목록
	if used["n"] {
		tl.Sources["n"] = Source{Kind: KindNcst, Label: "실황"}
	}
	expV := kst.Vilage.Latest(now.Add(-DelayGrace))
	for i, s := range vil {
		if !used[s.key] {
			continue
		}
		b := s.base
		src := Source{Kind: KindVilage, Label: "단기예보", Base: &b}
		if i == 0 && s.base.Before(expV) {
			src.Delayed = &Delayed{ExpectedBase: expV}
			tl.Warnings = append(tl.Warnings, "vilage_fallback")
		}
		tl.Sources[s.key] = src
	}
	if ult != nil && used[ult.key] {
		b := ult.base
		src := Source{Kind: KindUltra, Label: "초단기예보", Base: &b}
		if expU := kst.Ultra.Latest(now.Add(-DelayGrace)); ult.base.Before(expU) {
			src.Delayed = &Delayed{ExpectedBase: expU}
		}
		tl.Sources[ult.key] = src
	}
	if len(vil) == 0 {
		tl.Warnings = append(tl.Warnings, "no_vilage")
	}

	// 칸
	get := func(t time.Time) *hour { return hours[kst.ToInt(t)] }
	for t := start; t.Before(blockStart); t = t.Add(3 * time.Hour) {
		tl.Slots = append(tl.Slots, aggregate(get, t, "past"))
	}
	for t := blockStart; !t.After(todayLast); t = t.Add(time.Hour) {
		phase := "future"
		switch {
		case t.Before(nowHour):
			phase = "past"
		case t.Equal(nowHour):
			phase = "now"
		}
		tl.Slots = append(tl.Slots, single(get(t), t, phase))
	}
	for t := today.AddDate(0, 0, 1); !t.After(end); t = t.Add(3 * time.Hour) {
		if h := get(t); h != nil && h.ext {
			s := single(h, t, "future")
			s.H, s.Res = 3, "3h"
			tl.Slots = append(tl.Slots, s)
			continue
		}
		tl.Slots = append(tl.Slots, aggregate(get, t, "future"))
	}

	tl.Summary = summarize(order, get, now, nowHour)
	tl.Daily = daily(order, today, in.Place)
	tl.DataVersion = fmt.Sprintf("%d.%d:%s", in.Place.NX, in.Place.NY, nowHour.Format("200601021504"))
	if len(vil) > 0 {
		tl.DataVersion += fmt.Sprintf(":v%s.%d", vil[0].base.Format("200601021504"), vil[0].ver)
	}
	if ult != nil {
		tl.DataVersion += ":u" + ult.base.Format("200601021504")
	}
	return tl
}

func ultraAt(u *fsrc, t time.Time) map[string]string {
	if u == nil {
		return nil
	}
	return u.get(t)
}

func (h *hour) fillNcst(m map[string]string) {
	h.tmp, h.reh, h.wsd, h.vec, h.uuu, h.vvv = pf(m, "T1H"), pf(m, "REH"), pf(m, "WSD"), pf(m, "VEC"), pf(m, "UUU"), pf(m, "VVV")
	if p := pi(m, "PTY"); p != nil {
		h.pty, h.ptyKind = *p, KindNcst
	}
	if v, ok := m["RN1"]; ok {
		if a, err := value.ParsePCP(v, false); err == nil {
			h.pcp = &a
		}
	}
	h.obsSrc, h.pcpSrc = "n", "n"
}

func (h *hour) fillVilage(s *fsrc, m map[string]string, estimated bool) {
	h.tmp, h.reh, h.vec, h.uuu, h.vvv = pf(m, "TMP"), pf(m, "REH"), pf(m, "VEC"), pf(m, "UUU"), pf(m, "VVV")
	if h.ext {
		h.wsdCode = pi(m, "WSD")
		if h.uuu != nil && h.vvv != nil {
			w := round1(value.WindSpeed(*h.uuu, *h.vvv))
			h.wsd = &w
		}
	} else {
		h.wsd = pf(m, "WSD")
	}
	h.setVilagePrecip(s, m)
	h.obsSrc = s.key
	h.estimated = estimated
}

func (h *hour) setVilagePrecip(s *fsrc, m map[string]string) {
	h.pty, h.ptyKind, h.pcp = -1, "", nil
	if p := pi(m, "PTY"); p != nil {
		h.pty, h.ptyKind = *p, KindVilage
	}
	if v, ok := m["PCP"]; ok {
		if a, err := value.ParsePCP(v, h.ext); err == nil {
			h.pcp = &a
		}
	}
	h.pcpSrc = s.key
}

// fillUltra: 기온·바람·습도는 초단기. 강수형태·강수량은 발표 +2.5h까지만 초단기, 그 뒤는 단기(Q1).
func (h *hour) fillUltra(u *fsrc, m map[string]string, vs *fsrc, vm map[string]string) {
	h.tmp, h.reh, h.wsd, h.vec, h.uuu, h.vvv = pf(m, "T1H"), pf(m, "REH"), pf(m, "WSD"), pf(m, "VEC"), pf(m, "UUU"), pf(m, "VVV")
	h.obsSrc = u.key
	if !h.t.After(u.base.Add(UltraPrecipLead)) || vs == nil {
		if p := pi(m, "PTY"); p != nil {
			h.pty, h.ptyKind = *p, KindUltra
		}
		if v, ok := m["RN1"]; ok {
			if a, err := value.ParsePCP(v, false); err == nil {
				h.pcp = &a
			}
		}
		h.pcpSrc = u.key
		return
	}
	h.setVilagePrecip(vs, vm)
}

// --- 칸 만들기 ---

var ptyNames = map[int]string{0: "none", 1: "rain", 2: "rain_snow", 3: "snow", 4: "shower", 5: "drizzle", 6: "drizzle_snow", 7: "flurry"}

// ptyRank: 없음 < 빗방울날림·눈날림 < 빗방울 < 소나기 < 비 < 눈 < 비/눈 (설계 §3.1)
var ptyRank = map[int]int{0: 0, 6: 1, 7: 1, 5: 2, 4: 3, 1: 4, 3: 5, 2: 6}

var skyNames = map[int]string{1: "clear", 3: "partly", 4: "cloudy"}

func strp(s string) *string { return &s }

func single(h *hour, t time.Time, phase string) Slot {
	s := Slot{T: t, H: 1, Phase: phase, Res: "1h"}
	if h == nil {
		return s
	}
	s.Tmp, s.Reh, s.Wsd, s.WsdCode, s.Vec, s.Uuu, s.Vvv = h.tmp, h.reh, h.wsd, h.wsdCode, h.vec, h.uuu, h.vvv
	if h.vec != nil {
		s.Dir16 = strp(value.Dir16(*h.vec))
	}
	if h.pty >= 0 {
		if n, ok := ptyNames[h.pty]; ok {
			s.Pty = strp(n)
		}
	}
	s.Wet = h.wet()
	s.Pcp = amount(h.pcp, false)
	s.Sno = amount(h.sno, true)
	if n, ok := skyNames[h.sky]; ok {
		s.Sky = strp(n)
	}
	s.Pop = h.pop
	s.Src = SlotSrc{Obs: h.obsSrc, Pcp: h.pcpSrc, Sky: h.skySrc}
	s.Estimated = h.estimated
	s.Feels = feelsOf(h)
	return s
}

func feelsOf(h *hour) *float64 {
	if h == nil || h.tmp == nil {
		return nil
	}
	f := Feels(int(h.t.Month()), *h.tmp, h.wsd, h.reh)
	return &f
}

// aggregate는 t, t+1h, t+2h를 3시간 칸 하나로 합친다(설계 §3.1 표).
func aggregate(get func(time.Time) *hour, t time.Time, phase string) Slot {
	var hs []*hour
	missing := false
	for i := 0; i < 3; i++ {
		h := get(t.Add(time.Duration(i) * time.Hour))
		if h == nil || !h.hasObs() {
			missing = true
		}
		if h != nil {
			hs = append(hs, h)
		}
	}
	first := get(t)
	if first == nil || !first.hasObs() {
		// t 값이 없으면 칸 안의 첫 값을 쓰고 partial
		for _, h := range hs {
			if h.hasObs() {
				first = h
				break
			}
		}
	}
	var s Slot
	if first != nil {
		s = single(first, t, phase)
	}
	s.T, s.H, s.Res, s.Phase = t, 3, "1h", phase
	if len(hs) == 0 {
		return s
	}
	s.Partial = missing

	// 하늘상태는 t 값(없으면 첫 값)
	if s.Sky == nil {
		for _, h := range hs {
			if n, ok := skyNames[h.sky]; ok {
				s.Sky = strp(n)
				break
			}
		}
	}
	// 강수량·적설 합, 강수확률 최대, 강수형태 가장 센 것
	var pcps, snos []*value.Amount
	var pop *int
	for _, h := range hs {
		if h.pcp != nil {
			pcps = append(pcps, h.pcp)
		} else if h.hasObs() {
			s.Partial = true
		}
		if h.sno != nil {
			snos = append(snos, h.sno)
		}
		if h.pop != nil && (pop == nil || *h.pop > *pop) {
			v := *h.pop
			pop = &v
		}
		if h.obsSrc != s.Src.Obs || h.skySrc != s.Src.Sky || h.pcpSrc != s.Src.Pcp {
			s.Partial = true
		}
	}
	s.Pcp = amount(sum(pcps), false)
	s.Sno = amount(sum(snos), true)
	s.Pop = pop
	pty, wet := strongest(hs)
	s.Pty, s.Wet = pty, wet
	return s
}

// strongest는 칸의 강수형태다. 비·눈으로 볼 시각(wet)이 있으면 그중 가장 센 것(비와 눈이 섞이면 비/눈),
// 없으면 초단기 5·6·7 같은 약한 표시 중 가장 센 것을 이름으로만 남긴다(wet=false).
func strongest(hs []*hour) (*string, bool) {
	best, known := -1, false
	rain, snow := false, false
	wet := false
	for _, h := range hs {
		if h.pty < 0 {
			continue
		}
		known = true
		if h.wet() {
			if !wet {
				wet, best = true, -1
			}
			switch h.pty {
			case 1, 4, 5:
				rain = true
			case 3, 7:
				snow = true
			case 2, 6:
				rain, snow = true, true
			}
		} else if wet {
			continue
		}
		if best < 0 || ptyRank[h.pty] > ptyRank[best] {
			best = h.pty
		}
	}
	if !known {
		return nil, false
	}
	if wet && rain && snow {
		best = 2
	}
	return strp(ptyNames[best]), wet
}

func sum(as []*value.Amount) *value.Amount {
	if len(as) == 0 {
		return nil
	}
	out := value.Amount{Exact: true}
	hasMax := true
	var maxSum float64
	var code *int
	for _, a := range as {
		out.Min += a.Min
		out.Rep += a.Rep
		if a.Max == nil {
			hasMax = false
		} else {
			maxSum += *a.Max
		}
		if !a.Exact {
			out.Exact = false
		}
		if a.Code != nil && (code == nil || *a.Code > *code) {
			c := *a.Code
			code = &c
		}
	}
	if hasMax {
		out.Max = &maxSum
	}
	if code != nil {
		// 코드가 섞인 합은 쓰지 않는다(연장 구간은 합치지 않음). 가장 센 코드만 남긴다.
		out.Code = code
		out.Exact = false
	}
	return &out
}

func r1(v float64) float64 { return math.Round(v*10) / 10 }

func amount(a *value.Amount, snow bool) *Amount {
	if a == nil {
		return nil
	}
	out := &Amount{MM: r1(a.Rep), Min: r1(a.Min), Exact: a.Exact, Code: a.Code}
	if a.Max != nil {
		m := r1(*a.Max)
		out.Max = &m
	}
	out.Text = amountText(out, snow)
	return out
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func amountText(a *Amount, snow bool) string {
	unit := "mm"
	if snow {
		unit = "cm"
	}
	if a.Code != nil {
		if snow {
			switch *a.Code {
			case 1:
				return "보통 눈"
			case 2:
				return "많은 눈"
			}
			return "적설없음"
		}
		switch *a.Code {
		case 1:
			return "약한 비"
		case 2:
			return "보통 비"
		case 3:
			return "강한 비"
		}
		return "강수없음"
	}
	switch {
	case a.Max != nil && *a.Max == 0:
		if snow {
			return "적설없음"
		}
		return "강수없음"
	case a.Max == nil:
		return num(a.Min) + unit + " 이상"
	case a.Exact || a.Min == *a.Max:
		return num(*a.Max) + unit
	case a.Min == 0:
		return num(*a.Max) + unit + " 미만"
	}
	return num(a.Min) + "~" + num(*a.Max) + unit
}
