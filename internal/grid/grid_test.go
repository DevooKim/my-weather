package grid

import (
	"math"
	"testing"
)

func TestTableCounts(t *testing.T) {
	tb := MustLoad()
	if tb.Len() != 3838 || tb.Grids() != 1632 {
		t.Fatalf("regions=%d grids=%d, want 3838/1632", tb.Len(), tb.Grids())
	}
}

func TestKnownGrids(t *testing.T) {
	tb := MustLoad()
	for _, c := range []struct {
		code   string
		nx, ny int
	}{{"1144066000", 59, 126}, {"1168064000", 61, 125}, {"5176038000", 89, 130}} {
		r := tb.ByCode(c.code)
		if r == nil {
			t.Fatalf("%s missing", c.code)
		}
		if r.NX != c.nx || r.NY != c.ny {
			t.Fatalf("%s table grid %d,%d", c.code, r.NX, r.NY)
		}
		nx, ny, ok := ToGrid(r.Lat, r.Lon)
		if !ok || nx != c.nx || ny != c.ny {
			t.Fatalf("%s LCC %d,%d ok=%v want %d,%d", r.Full(), nx, ny, ok, c.nx, c.ny)
		}
	}
}

// 표의 대표 좌표를 LCC로 다시 계산해 표의 격자와 비교한다. 경계 근처 좌표는 반올림 차이로 어긋날 수 있어 상한만 건다.
func TestLCCAgainstTable(t *testing.T) {
	tb := MustLoad()
	n, bad, far := 0, 0, 0
	for _, r := range tb.All() {
		if !r.HasPos {
			continue
		}
		n++
		nx, ny, ok := ToGrid(r.Lat, r.Lon)
		if !ok || nx != r.NX || ny != r.NY {
			bad++
			if !ok || abs(nx-r.NX) > 1 || abs(ny-r.NY) > 1 {
				far++
			}
		}
	}
	t.Logf("LCC mismatches %d/%d (off by more than 1: %d)", bad, n, far)
	// 2026-11-10 측정: 43/3836 (1.1%), 모두 1칸 차이. 표의 대표 좌표가 격자 경계 근처이거나 좌표만 갱신된 행으로 보인다.
	if bad*50 > n || far > 0 {
		t.Fatalf("too many mismatches: %d/%d, far=%d", bad, n, far)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func TestInverseRoundTrip(t *testing.T) {
	for _, g := range [][2]int{{59, 126}, {61, 125}, {89, 130}, {1, 1}, {149, 253}, {52, 38}} {
		lat, lon := ToLatLon(g[0], g[1])
		nx, ny, ok := ToGrid(lat, lon)
		if !ok || nx != g[0] || ny != g[1] {
			t.Fatalf("%v → %.4f,%.4f → %d,%d %v", g, lat, lon, nx, ny, ok)
		}
	}
}

func TestOutside(t *testing.T) {
	for _, ll := range [][2]float64{{35.68, 139.69} /*도쿄*/, {0, 0}, {48.85, 2.35}, {math.NaN(), 127}} {
		if _, _, ok := ToGrid(ll[0], ll[1]); ok {
			t.Fatalf("%v should be outside", ll)
		}
	}
}

func TestNearest(t *testing.T) {
	tb := MustLoad()
	r, d := tb.Nearest(37.5497, 126.9230)
	if r == nil || r.L3 == "" || d > 3 {
		t.Fatalf("nearest = %v %.2f", r, d)
	}
	if nx, ny, _ := ToGrid(37.5497, 126.9230); nx != 59 || ny != 126 {
		t.Fatalf("grid %d,%d", nx, ny)
	}
}

func TestSearchRanking(t *testing.T) {
	tb := MustLoad()
	got := tb.Search("서교", 5)
	if len(got) == 0 || got[0].Code != "1144066000" {
		t.Fatalf("서교 → %v", got)
	}
	// 공백 무시
	got = tb.Search("마포구", 50)
	if len(got) == 0 {
		t.Fatal("no hit")
	}
	for _, r := range got {
		if r.L2 != "마포구" {
			t.Fatalf("unexpected %s", r.Full())
		}
	}
	if len(tb.Search("  ", 10)) != 0 || len(tb.Search("없는지명xyz", 10)) != 0 {
		t.Fatal("empty expected")
	}
	if n := len(tb.Search("동", 7)); n != 7 {
		t.Fatalf("limit: %d", n)
	}
}

func TestForGrid(t *testing.T) {
	tb := MustLoad()
	r := tb.ForGrid(59, 126)
	if r == nil || r.NX != 59 || r.NY != 126 || r.L3 == "" {
		t.Fatalf("ForGrid = %+v", r)
	}
	if tb.ForGrid(1, 1) != nil {
		t.Fatal("1,1 should have no region")
	}
}
