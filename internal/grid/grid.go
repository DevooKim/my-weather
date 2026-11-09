// Package grid는 위경도↔기상청 격자(LCC) 변환과 행정구역 표(embed), 지역 검색, 가장 가까운 지명을 담당한다.
//
// 행정구역 표는 활용가이드 부록 xlsx를 gen 명령으로 바꾼 regions.csv다(3,838행, 고유 격자 1,632개).
package grid

//go:generate go run ./gen -in $XLSX -out regions.csv

import (
	"bytes"
	_ "embed"
	"encoding/csv"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// 격자 범위 (설계 §2.0)
const (
	MaxX = 149
	MaxY = 253
)

// 기상청 LCC 상수: 지구 반경 6371.00877km, 격자 5km, 표준위도 30·60°, 기준점 (126°E, 38°N) = 격자 (43, 136).
const (
	earthRadius = 6371.00877
	gridKm      = 5.0
	slat1       = 30.0
	slat2       = 60.0
	olon        = 126.0
	olat        = 38.0
	xo          = 43.0
	yo          = 136.0
)

var lcc = func() (p struct{ re, sn, sf, ro float64 }) {
	const d = math.Pi / 180
	p.re = earthRadius / gridKm
	s1, s2, ol := slat1*d, slat2*d, olat*d
	sn := math.Tan(math.Pi*0.25+s2*0.5) / math.Tan(math.Pi*0.25+s1*0.5)
	p.sn = math.Log(math.Cos(s1)/math.Cos(s2)) / math.Log(sn)
	sf := math.Tan(math.Pi*0.25 + s1*0.5)
	p.sf = math.Pow(sf, p.sn) * math.Cos(s1) / p.sn
	ro := math.Tan(math.Pi*0.25 + ol*0.5)
	p.ro = p.re * p.sf / math.Pow(ro, p.sn)
	return p
}()

// ToGrid는 위경도를 격자로 바꾼다. 격자 범위(1~149, 1~253) 밖이면 ok=false.
func ToGrid(lat, lon float64) (nx, ny int, ok bool) {
	if math.IsNaN(lat) || math.IsNaN(lon) || lat < -89 || lat > 89 || lon < -180 || lon > 180 {
		return 0, 0, false
	}
	const d = math.Pi / 180
	ra := math.Tan(math.Pi*0.25 + lat*d*0.5)
	ra = lcc.re * lcc.sf / math.Pow(ra, lcc.sn)
	theta := lon*d - olon*d
	if theta > math.Pi {
		theta -= 2 * math.Pi
	}
	if theta < -math.Pi {
		theta += 2 * math.Pi
	}
	theta *= lcc.sn
	x := math.Floor(ra*math.Sin(theta) + xo + 0.5)
	y := math.Floor(lcc.ro - ra*math.Cos(theta) + yo + 0.5)
	if x < 1 || x > MaxX || y < 1 || y > MaxY {
		return 0, 0, false
	}
	return int(x), int(y), true
}

// ToLatLon은 격자 중심의 위경도다(역변환).
func ToLatLon(nx, ny int) (lat, lon float64) {
	const d = math.Pi / 180
	xn := float64(nx) - xo
	yn := lcc.ro - float64(ny) + yo
	ra := math.Sqrt(xn*xn + yn*yn)
	if lcc.sn < 0 {
		ra = -ra
	}
	alat := math.Pow(lcc.re*lcc.sf/ra, 1/lcc.sn)
	alat = 2*math.Atan(alat) - math.Pi*0.5
	var theta float64
	switch {
	case math.Abs(xn) <= 0:
		theta = 0
	case math.Abs(yn) <= 0:
		theta = math.Pi * 0.5
		if xn < 0 {
			theta = -theta
		}
	default:
		theta = math.Atan2(xn, yn)
	}
	alon := theta/lcc.sn + olon*d
	return alat / d, alon / d
}

// Valid는 격자 번호가 범위 안인지다.
func Valid(nx, ny int) bool { return nx >= 1 && nx <= MaxX && ny >= 1 && ny <= MaxY }

// Region은 행정구역 한 줄이다. 위경도가 없는 행(이어도)은 HasPos=false.
type Region struct {
	Code       string
	L1, L2, L3 string
	NX, NY     int
	Lat, Lon   float64
	HasPos     bool
	key        string // 공백을 지운 전체 이름 (검색용)
	l2key      string
	l3key      string
}

// Name은 가장 아래 단계 이름이다.
func (r *Region) Name() string {
	switch {
	case r.L3 != "":
		return r.L3
	case r.L2 != "":
		return r.L2
	}
	return r.L1
}

// Full은 "서울특별시 마포구 서교동" 형식의 전체 이름이다.
func (r *Region) Full() string {
	parts := []string{r.L1}
	if r.L2 != "" {
		parts = append(parts, r.L2)
	}
	if r.L3 != "" {
		parts = append(parts, r.L3)
	}
	return strings.Join(parts, " ")
}

//go:embed regions.csv
var regionsCSV []byte

// Table은 행정구역 표와 색인이다. 만든 뒤에는 읽기만 하므로 동시에 써도 안전하다.
type Table struct {
	all    []*Region
	byCode map[string]*Region
	byGrid map[[2]int][]*Region
}

// Load는 embed된 표를 읽는다.
func Load() (*Table, error) { return parse(regionsCSV) }

// MustLoad는 Load가 실패하면 panic한다(embed 파일 오류는 빌드 결함).
func MustLoad() *Table {
	t, err := Load()
	if err != nil {
		panic(err)
	}
	return t
}

func parse(b []byte) (*Table, error) {
	rd := csv.NewReader(bytes.NewReader(b))
	recs, err := rd.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(recs) < 2 || strings.Join(recs[0], ",") != "code,l1,l2,l3,nx,ny,lat,lon" {
		return nil, fmt.Errorf("grid: bad regions.csv header")
	}
	t := &Table{byCode: map[string]*Region{}, byGrid: map[[2]int][]*Region{}}
	for i, r := range recs[1:] {
		nx, err1 := strconv.Atoi(r[4])
		ny, err2 := strconv.Atoi(r[5])
		if err1 != nil || err2 != nil || !Valid(nx, ny) {
			return nil, fmt.Errorf("grid: regions.csv line %d: bad grid", i+2)
		}
		reg := &Region{Code: r[0], L1: r[1], L2: r[2], L3: r[3], NX: nx, NY: ny}
		if r[6] != "" {
			lat, err1 := strconv.ParseFloat(r[6], 64)
			lon, err2 := strconv.ParseFloat(r[7], 64)
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("grid: regions.csv line %d: bad lat/lon", i+2)
			}
			reg.Lat, reg.Lon, reg.HasPos = lat, lon, true
		}
		reg.key = Normalize(reg.L1 + reg.L2 + reg.L3)
		reg.l2key = Normalize(reg.L2)
		reg.l3key = Normalize(reg.L3)
		t.all = append(t.all, reg)
		t.byCode[reg.Code] = reg
		g := [2]int{nx, ny}
		t.byGrid[g] = append(t.byGrid[g], reg)
	}
	return t, nil
}

// Len은 행정구역 수다.
func (t *Table) Len() int { return len(t.all) }

// Grids는 고유 격자 수다.
func (t *Table) Grids() int { return len(t.byGrid) }

// All은 모든 행정구역이다(테스트용, 바꾸지 말 것).
func (t *Table) All() []*Region { return t.all }

// ByCode는 행정구역 코드로 찾는다.
func (t *Table) ByCode(code string) *Region { return t.byCode[code] }

// ForGrid는 격자의 대표 지명이다. 동(3단계) 행 중 격자 중심에 가장 가까운 것, 없으면 그 격자의 아무 행.
// 표에 없는 격자면 nil.
func (t *Table) ForGrid(nx, ny int) *Region {
	rs := t.byGrid[[2]int{nx, ny}]
	if len(rs) == 0 {
		return nil
	}
	clat, clon := ToLatLon(nx, ny)
	var best *Region
	bestD := math.Inf(1)
	for _, r := range rs {
		if r.L3 == "" || !r.HasPos {
			continue
		}
		if d := DistanceKm(clat, clon, r.Lat, r.Lon); d < bestD {
			best, bestD = r, d
		}
	}
	if best == nil {
		best = rs[0]
	}
	return best
}

// Nearest는 위경도에서 가장 가까운 동(3단계) 지명과 거리(km)다. 위경도가 없는 행은 뺀다.
func (t *Table) Nearest(lat, lon float64) (*Region, float64) {
	var best *Region
	bestD := math.Inf(1)
	for _, r := range t.all {
		if r.L3 == "" || !r.HasPos {
			continue
		}
		if d := DistanceKm(lat, lon, r.Lat, r.Lon); d < bestD {
			best, bestD = r, d
		}
	}
	return best, bestD
}

// Normalize는 검색용으로 공백을 지우고 소문자로 바꾼다.
func Normalize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, s)
}

// Search는 공백을 무시한 부분 일치로 찾는다(설계 §2.3).
// 순서: 동 이름 앞부분 일치 > 시군구 일치 > 그 외. 같은 순위 안에서는 단계가 낮은(동) 것, 그다음 표 순서.
func (t *Table) Search(q string, limit int) []*Region {
	nq := Normalize(q)
	if nq == "" || limit <= 0 {
		return nil
	}
	type hit struct {
		r    *Region
		rank int
		i    int
	}
	var hits []hit
	for i, r := range t.all {
		if !strings.Contains(r.key, nq) {
			continue
		}
		rank := 2
		switch {
		case r.l3key != "" && strings.HasPrefix(r.l3key, nq):
			rank = 0
		case r.l2key != "" && strings.Contains(r.l2key, nq):
			rank = 1
		}
		hits = append(hits, hit{r, rank, i})
	}
	sort.SliceStable(hits, func(a, b int) bool {
		if hits[a].rank != hits[b].rank {
			return hits[a].rank < hits[b].rank
		}
		return hits[a].i < hits[b].i
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]*Region, len(hits))
	for i, h := range hits {
		out[i] = h.r
	}
	return out
}

// DistanceKm는 두 위경도 사이의 대원 거리(km)다.
func DistanceKm(lat1, lon1, lat2, lon2 float64) float64 {
	const d = math.Pi / 180
	const r = 6371.0
	dlat := (lat2 - lat1) * d
	dlon := (lon2 - lon1) * d
	a := math.Sin(dlat/2)*math.Sin(dlat/2) + math.Cos(lat1*d)*math.Cos(lat2*d)*math.Sin(dlon/2)*math.Sin(dlon/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}

// NearestInGrid는 격자 (nx, ny) 안의 동(3단계) 지명 중 위경도에서 가장 가까운 것이다. 없으면 nil.
func (t *Table) NearestInGrid(nx, ny int, lat, lon float64) (*Region, float64) {
	var best *Region
	bestD := math.Inf(1)
	for _, r := range t.byGrid[[2]int{nx, ny}] {
		if r.L3 == "" || !r.HasPos {
			continue
		}
		if d := DistanceKm(lat, lon, r.Lat, r.Lon); d < bestD {
			best, bestD = r, d
		}
	}
	return best, bestD
}
