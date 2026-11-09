package timeline

import "math"

// Feels는 체감온도(℃)다(설계 §3.5).
//
//   - 겨울(10~4월): 기온 ≤ 10℃이고 풍속 ≥ 1.3m/s이면 기상청 겨울철 체감온도(바람냉각) 식.
//     13.12 + 0.6215·T − 11.37·V^0.16 + 0.3965·V^0.16·T, V는 km/h(= m/s × 3.6). 조건 밖이면 기온 그대로.
//   - 여름(5~9월): 기상청 여름철 체감온도. Tw = Stull(2011) 습구온도 근사,
//     −0.2442 + 0.55399·Tw + 0.45535·T − 0.0022·Tw² + 0.00278·Tw·T + 3.0. 습도가 없으면 기온 그대로.
//
// wsd·reh가 nil이면 해당 계산을 하지 않고 기온을 돌려준다. 결과는 소수 첫째 자리로 반올림.
func Feels(month int, tmp float64, wsd, reh *float64) float64 {
	summer := month >= 5 && month <= 9
	if summer {
		if reh == nil {
			return round1(tmp)
		}
		tw := stullWetBulb(tmp, *reh)
		return round1(-0.2442 + 0.55399*tw + 0.45535*tmp - 0.0022*tw*tw + 0.00278*tw*tmp + 3.0)
	}
	if wsd == nil || tmp > 10 || *wsd < 1.3 {
		return round1(tmp)
	}
	v := math.Pow(*wsd*3.6, 0.16)
	return round1(13.12 + 0.6215*tmp - 11.37*v + 0.3965*v*tmp)
}

// stullWetBulb는 Stull(2011) 습구온도 근사다(T ℃, RH %).
func stullWetBulb(t, rh float64) float64 {
	return t*math.Atan(0.151977*math.Sqrt(rh+8.313659)) +
		math.Atan(t+rh) - math.Atan(rh-1.67633) +
		0.00391838*math.Pow(rh, 1.5)*math.Atan(0.023101*rh) - 4.686035
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
