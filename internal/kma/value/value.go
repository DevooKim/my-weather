// Package value는 기상청 응답 값(PCP/SNO/WSD 문자열·코드, PTY/SKY 코드)을 정규화한다.
//
// 저장은 원문 그대로 하고(설계 §4.1), 이 패키지는 읽을 때 쓴다.
package value

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Amount는 강수량(mm) 또는 적설(cm)의 정규화 결과다(설계 §3.4).
// Max가 nil이면 상한이 없다("50.0mm 이상").
type Amount struct {
	Min   float64
	Max   *float64
	Rep   float64 // 대표값 (mm 또는 cm)
	Exact bool
	Code  *int // 단기예보 연장 구간의 코드값일 때만
}

func f(v float64) *float64 { return &v }

func compact(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == ' ' {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
}

// ParsePCP는 강수량 문자열을 정규화한다. isCode는 단기예보 연장 구간(3시간 간격)의 코드값인지 여부다.
// 같은 "1"이라도 늦은 발표분 D+3 1시간 구간에서는 1mm, 연장 구간에서는 코드 1이므로 호출자가 구분한다.
func ParsePCP(s string, isCode bool) (Amount, error) {
	c := compact(s)
	if isCode {
		return pcpCode(c)
	}
	switch {
	case c == "" || c == "강수없음" || c == "0" || c == "-":
		return Amount{Max: f(0), Exact: true}, nil
	case strings.HasSuffix(c, "미만"): // 1mm미만, 1.0mm미만
		v, err := num(strings.TrimSuffix(strings.TrimSuffix(c, "미만"), "mm"))
		if err != nil {
			return Amount{}, fmt.Errorf("pcp %q: %w", s, err)
		}
		return Amount{Min: 0, Max: f(v), Rep: v / 2}, nil
	case strings.HasSuffix(c, "이상"): // 50.0mm이상
		v, err := num(strings.TrimSuffix(strings.TrimSuffix(c, "이상"), "mm"))
		if err != nil {
			return Amount{}, fmt.Errorf("pcp %q: %w", s, err)
		}
		return Amount{Min: v, Rep: v}, nil
	case strings.Contains(c, "~"): // 30.0~50.0mm
		parts := strings.SplitN(strings.TrimSuffix(c, "mm"), "~", 2)
		lo, err1 := num(strings.TrimSuffix(parts[0], "mm"))
		hi, err2 := num(parts[1])
		if err1 != nil || err2 != nil {
			return Amount{}, fmt.Errorf("pcp %q: bad range", s)
		}
		return Amount{Min: lo, Max: f(hi), Rep: (lo + hi) / 2}, nil
	default: // 2.0mm, 0.7
		v, err := num(strings.TrimSuffix(c, "mm"))
		if err != nil {
			return Amount{}, fmt.Errorf("pcp %q: %w", s, err)
		}
		return Amount{Min: v, Max: f(v), Rep: v, Exact: true}, nil
	}
}

// pcpCode: 활용가이드 2609판 '단기예보 연장에 따른 코드값 정보'(설계 §8.1 Q12).
// 1 약한 비(<3mm/h), 2 보통 비(3~15mm/h), 3 강한 비(≥15mm/h). 0은 가이드에 없지만 측정에서 보였으므로 강수없음으로 둔다.
func pcpCode(c string) (Amount, error) {
	n, err := strconv.Atoi(c)
	if err != nil {
		return Amount{}, fmt.Errorf("pcp code %q: %w", c, err)
	}
	a := Amount{Code: &n}
	switch n {
	case 0:
		a.Exact = true
	case 1:
		a.Max, a.Rep = f(3), 1.5
	case 2:
		a.Min, a.Max, a.Rep = 3, f(15), 9
	case 3:
		a.Min, a.Rep = 15, 15
	default:
		return Amount{}, fmt.Errorf("pcp code %d out of range", n)
	}
	return a, nil
}

// ParseSNO는 적설 문자열을 정규화한다. isCode는 연장 구간 코드값 여부다.
func ParseSNO(s string, isCode bool) (Amount, error) {
	c := compact(s)
	if isCode {
		n, err := strconv.Atoi(c)
		if err != nil {
			return Amount{}, fmt.Errorf("sno code %q: %w", s, err)
		}
		a := Amount{Code: &n}
		switch n {
		case 0:
			a.Exact = true
		case 1: // 보통 눈 (<1cm/h)
			a.Max, a.Rep = f(1), 0.5
		case 2: // 많은 눈 (≥1cm/h)
			a.Min, a.Rep = 1, 1
		default:
			return Amount{}, fmt.Errorf("sno code %d out of range", n)
		}
		return a, nil
	}
	switch {
	case c == "" || c == "적설없음" || c == "0" || c == "-":
		return Amount{Max: f(0), Exact: true}, nil
	case strings.HasSuffix(c, "미만"): // 0.5cm미만, 1cm미만
		v, err := num(strings.TrimSuffix(strings.TrimSuffix(c, "미만"), "cm"))
		if err != nil {
			return Amount{}, fmt.Errorf("sno %q: %w", s, err)
		}
		return Amount{Max: f(v), Rep: v / 2}, nil
	case strings.HasSuffix(c, "이상"):
		v, err := num(strings.TrimSuffix(strings.TrimSuffix(c, "이상"), "cm"))
		if err != nil {
			return Amount{}, fmt.Errorf("sno %q: %w", s, err)
		}
		return Amount{Min: v, Rep: v}, nil
	case strings.Contains(c, "~"):
		parts := strings.SplitN(strings.TrimSuffix(c, "cm"), "~", 2)
		lo, err1 := num(strings.TrimSuffix(parts[0], "cm"))
		hi, err2 := num(parts[1])
		if err1 != nil || err2 != nil {
			return Amount{}, fmt.Errorf("sno %q: bad range", s)
		}
		return Amount{Min: lo, Max: f(hi), Rep: (lo + hi) / 2}, nil
	default:
		v, err := num(strings.TrimSuffix(c, "cm"))
		if err != nil {
			return Amount{}, fmt.Errorf("sno %q: %w", s, err)
		}
		return Amount{Min: v, Max: f(v), Rep: v, Exact: true}, nil
	}
}

func num(s string) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("not a number: %q", s)
	}
	return v, nil
}

// PTY 코드표. 실황·초단기는 0,1,2,3,5,6,7, 단기는 0,1,2,3,4.
var ptyNames = map[int]string{
	0: "none", 1: "rain", 2: "rain_snow", 3: "snow", 4: "shower",
	5: "drizzle", 6: "drizzle_snow", 7: "flurry",
}

// PTY는 강수형태 코드 이름이다.
func PTY(code string) (string, error) {
	n, err := strconv.Atoi(compact(code))
	if err != nil {
		return "", fmt.Errorf("pty %q: %w", code, err)
	}
	name, ok := ptyNames[n]
	if !ok {
		return "", fmt.Errorf("pty %d unknown", n)
	}
	return name, nil
}

// IsRain은 비로 표시할 강수형태인지다. 초단기 PTY 5·6·7(빗방울·날림)은 비로 보지 않는다(설계 §8.1 Q1).
func IsRain(code string) bool {
	n, err := strconv.Atoi(compact(code))
	return err == nil && n >= 1 && n <= 4
}

// SKY는 하늘상태 코드 이름이다: 1 clear, 3 partly, 4 cloudy.
func SKY(code string) (string, error) {
	switch compact(code) {
	case "1":
		return "clear", nil
	case "3":
		return "partly", nil
	case "4":
		return "cloudy", nil
	}
	return "", fmt.Errorf("sky %q unknown", code)
}

// WSDCode는 연장 구간 풍속 코드(1~3)의 구간(m/s)이다. 상한이 없으면 hi는 nil.
// 1 약한 바람(<4), 2 약간 강한 바람(4~9), 3 강한 바람(≥9).
func WSDCode(code string) (lo float64, hi *float64, err error) {
	switch compact(code) {
	case "1":
		return 0, f(4), nil
	case "2":
		return 4, f(9), nil
	case "3":
		return 9, nil, nil
	}
	return 0, nil, fmt.Errorf("wsd code %q unknown", code)
}

// WindSpeed는 UUU·VVV로 풍속을 계산한다(측정 오차 ±0.22m/s).
func WindSpeed(uuu, vvv float64) float64 { return math.Hypot(uuu, vvv) }

var dirs16 = [16]string{"N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE", "S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW"}

// Dir16은 VEC(도)를 16방위로 바꾼다: floor((VEC + 11.25) / 22.5) mod 16.
func Dir16(vec float64) string {
	i := int(math.Floor((vec+11.25)/22.5)) % 16
	if i < 0 {
		i += 16
	}
	return dirs16[i]
}
