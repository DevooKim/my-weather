package timeline

import (
	"math"
	"testing"
)

func fp(v float64) *float64 { return &v }

func TestFeelsWinter(t *testing.T) {
	// 환경캐나다·기상청 바람냉각 표: −5℃, 20km/h → −11.6℃ (V는 km/h).
	if got := Feels(1, -5, fp(20/3.6), nil); math.Abs(got-(-11.6)) > 0.1 {
		t.Fatalf("winter -5℃ 20km/h = %v, want ≈ -11.6", got)
	}
	// 10℃ 초과·약한 바람이면 기온 그대로
	if got := Feels(12, 11, fp(5), nil); got != 11 {
		t.Fatalf("got %v", got)
	}
	if got := Feels(12, 3, fp(1.0), nil); got != 3 {
		t.Fatalf("calm: got %v", got)
	}
	if got := Feels(12, 3, nil, nil); got != 3 {
		t.Fatalf("no wind: got %v", got)
	}
}

func TestStullWetBulb(t *testing.T) {
	// Stull(2011) 논문의 검증값: T=20℃, RH=50% → Tw ≈ 13.7℃
	if tw := stullWetBulb(20, 50); math.Abs(tw-13.7) > 0.1 {
		t.Fatalf("Tw = %v", tw)
	}
}

func TestFeelsSummer(t *testing.T) {
	// 30℃·70% → 약 31.3℃ (기상청 여름철 체감온도 표 값과 같은 수준)
	got := Feels(7, 30, nil, fp(70))
	tw := stullWetBulb(30, 70)
	want := -0.2442 + 0.55399*tw + 0.45535*30 - 0.0022*tw*tw + 0.00278*tw*30 + 3.0
	if math.Abs(got-want) > 0.06 || got < 30.8 || got > 31.8 {
		t.Fatalf("summer = %v (want %.2f)", got, want)
	}
	if got := Feels(7, 30, nil, nil); got != 30 {
		t.Fatalf("no reh: %v", got)
	}
}
