// Package astro는 일출·일몰 시각과 달 위상을 계산한다(API 없음, 설계 §3.7).
//
// 일출·일몰은 일출 방정식(NOAA 근사, 대기 굴절·태양 반지름 보정 −0.833°)을 쓴다. 오차는 1~2분 수준이다.
// 달 위상은 달과 해의 이각(Meeus 『Astronomical Algorithms』 48장 간이식)으로 구한다(0 = 삭, 0.5 = 망).
// 이각 오차는 0.5° 안팎, 시각으로 1시간 안쪽이다.
package astro

import (
	"math"
	"time"
)

const deg = math.Pi / 180

// SunTimes는 date(그 위치의 날짜, loc 기준)의 일출·일몰이다.
// 극야·백야로 해가 뜨거나 지지 않으면 ok=false.
func SunTimes(date time.Time, lat, lon float64, loc *time.Location) (rise, set time.Time, ok bool) {
	y, m, d := date.In(loc).Date()
	// 그날 정오(현지)를 기준으로 J2000 이후 날 수
	noon := time.Date(y, m, d, 12, 0, 0, 0, loc)
	jd := julian(noon)
	n := math.Round(jd - 2451545.0 + 0.0008)
	jstar := n - lon/360
	M := math.Mod(357.5291+0.98560028*jstar, 360)
	C := 1.9148*math.Sin(M*deg) + 0.0200*math.Sin(2*M*deg) + 0.0003*math.Sin(3*M*deg)
	lambda := math.Mod(M+C+180+102.9372, 360)
	jtransit := 2451545.0 + jstar + 0.0053*math.Sin(M*deg) - 0.0069*math.Sin(2*lambda*deg)
	sinDecl := math.Sin(lambda*deg) * math.Sin(23.4397*deg)
	cosDecl := math.Cos(math.Asin(sinDecl))
	cosW := (math.Sin(-0.833*deg) - math.Sin(lat*deg)*sinDecl) / (math.Cos(lat*deg) * cosDecl)
	if cosW < -1 || cosW > 1 {
		return time.Time{}, time.Time{}, false
	}
	w := math.Acos(cosW) / deg
	rise = fromJulian(jtransit - w/360).In(loc).Truncate(time.Minute)
	set = fromJulian(jtransit + w/360).In(loc).Truncate(time.Minute)
	return rise, set, true
}

func julian(t time.Time) float64 {
	return float64(t.Unix())/86400 + 2440587.5
}

func fromJulian(j float64) time.Time {
	sec := (j - 2440587.5) * 86400
	return time.Unix(int64(math.Round(sec)), 0)
}

// MoonPhase는 t의 달 위상(0~1)이다. 0 = 삭(그믐), 0.25 = 상현, 0.5 = 망(보름), 0.75 = 하현.
// 비친 비율은 (1 − cos 2πp) / 2.
//
// 달 공전 속도가 일정하지 않아 평균 삭망월로 나누면 실제 삭과 반나절 넘게 어긋난다.
// 그래서 평균 이각 D에 달·해 궤도 이심률에 따른 주요 보정항을 더한 참 이각을 쓴다(Meeus 48.4의 위상각 i = 180° − 참 이각).
func MoonPhase(t time.Time) float64 {
	T := (julian(t) - 2451545.0) / 36525                                                   // J2000 이후 율리우스 세기
	D := 297.8501921 + 445267.1114034*T - 0.0018819*T*T + T*T*T/545868 - T*T*T*T/113065000 // 달의 평균 이각
	M := 357.5291092 + 35999.0502909*T - 0.0001536*T*T + T*T*T/24490000                    // 해의 평균 근점이각
	Mp := 134.9633964 + 477198.8675055*T + 0.0087414*T*T + T*T*T/69699 - T*T*T*T/14712000  // 달의 평균 근점이각
	D, M, Mp = D*deg, M*deg, Mp*deg
	e := D/deg +
		6.289*math.Sin(Mp) -
		2.100*math.Sin(M) +
		1.274*math.Sin(2*D-Mp) +
		0.658*math.Sin(2*D) +
		0.214*math.Sin(2*Mp) +
		0.110*math.Sin(D)
	p := math.Mod(e/360, 1)
	if p < 0 {
		p++
	}
	return math.Round(p*1000) / 1000
}
