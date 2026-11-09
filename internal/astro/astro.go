// Package astro는 일출·일몰 시각과 달 위상을 계산한다(API 없음, 설계 §3.7).
//
// 일출·일몰은 일출 방정식(NOAA 근사, 대기 굴절·태양 반지름 보정 −0.833°)을 쓴다. 오차는 1~2분 수준이다.
// 달 위상은 기준 삭(2000-01-06 18:14 UTC)부터 평균 삭망월로 나눈 값이다(0 = 삭, 0.5 = 망). 오차는 반나절 안팎이다.
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

// 평균 삭망월(일)과 기준 삭
const synodic = 29.530588853

var refNewMoon = time.Date(2000, 1, 6, 18, 14, 0, 0, time.UTC)

// MoonPhase는 t의 달 위상(0~1)이다. 0 = 삭(그믐), 0.25 = 상현, 0.5 = 망(보름), 0.75 = 하현.
func MoonPhase(t time.Time) float64 {
	days := t.Sub(refNewMoon).Hours() / 24
	p := math.Mod(days/synodic, 1)
	if p < 0 {
		p++
	}
	return math.Round(p*1000) / 1000
}
