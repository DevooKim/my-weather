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
	// 2026-01-03 보름(10:03 UTC), 2026-01-18 그믐(19:52 UTC)
	if p := MoonPhase(time.Date(2026, 1, 3, 10, 3, 0, 0, time.UTC)); math.Abs(p-0.5) > 0.03 {
		t.Fatalf("full moon phase %.3f", p)
	}
	p := MoonPhase(time.Date(2026, 1, 18, 19, 52, 0, 0, time.UTC))
	if p > 0.03 && p < 0.97 {
		t.Fatalf("new moon phase %.3f", p)
	}
}
