package astro

import (
	"math"
	"testing"
	"time"

	"myweather/internal/kst"
)

func near(t *testing.T, got time.Time, want string, tol time.Duration) {
	t.Helper()
	w, err := time.ParseInLocation("2006-01-02 15:04", want, kst.Loc)
	if err != nil {
		t.Fatal(err)
	}
	if d := got.Sub(w); d > tol || d < -tol {
		t.Fatalf("got %s want %s (±%s)", got.Format("2006-01-02 15:04"), want, tol)
	}
}

// 기준값: 한국천문연구원 출몰시각(서울, 2026-11-10 일출 07:05·일몰 17:25 전후)과
// 하지·동지 무렵의 잘 알려진 서울 값. 근사식이므로 3분 허용.
func TestSeoulSun(t *testing.T) {
	lat, lon := 37.5665, 126.978
	r, s, ok := SunTimes(time.Date(2026, 11, 10, 0, 0, 0, 0, kst.Loc), lat, lon, kst.Loc)
	if !ok {
		t.Fatal("no sun")
	}
	near(t, r, "2026-11-10 07:05", 4*time.Minute)
	near(t, s, "2026-11-10 17:25", 4*time.Minute)

	r, s, _ = SunTimes(time.Date(2026, 6, 21, 0, 0, 0, 0, kst.Loc), lat, lon, kst.Loc)
	near(t, r, "2026-06-21 05:11", 4*time.Minute)
	near(t, s, "2026-06-21 19:57", 4*time.Minute)
}

func TestPolar(t *testing.T) {
	if _, _, ok := SunTimes(time.Date(2026, 12, 21, 0, 0, 0, 0, time.UTC), 80, 0, time.UTC); ok {
		t.Fatal("polar night should be !ok")
	}
}

func TestMoonPhase(t *testing.T) {
	// 실제 삭·망 시각(UTC). 평균 삭망월로는 반나절 넘게 어긋나던 달(2026-11)도 넣는다.
	at := func(s string) time.Time {
		v, err := time.Parse("2006-01-02 15:04", s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	// 0.01 ≈ 7시간
	near := func(p, want float64) bool {
		d := math.Abs(p - want)
		return math.Min(d, 1-d) <= 0.01
	}
	for _, c := range []struct {
		t    string
		want float64
	}{
		{"2026-01-03 10:03", 0.5},
		{"2026-01-18 19:52", 0},
		{"2026-11-09 07:02", 0},
		{"2026-11-24 14:53", 0.5},
	} {
		if p := MoonPhase(at(c.t)); !near(p, c.want) {
			t.Errorf("%s: phase %.3f, want %.2f", c.t, p, c.want)
		}
	}
	// 차는 달·기우는 달 방향
	if p := MoonPhase(at("2026-11-13 00:00")); p <= 0 || p >= 0.25 {
		t.Errorf("waxing crescent phase %.3f", p)
	}
}
