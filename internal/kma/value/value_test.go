package value

import "testing"

func eqp(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func TestParsePCP(t *testing.T) {
	cases := []struct {
		in     string
		code   bool
		min    float64
		max    *float64
		rep    float64
		exact  bool
		hasErr bool
	}{
		{"강수없음", false, 0, f(0), 0, true, false},
		{"0", false, 0, f(0), 0, true, false},
		{"1mm 미만", false, 0, f(1), 0.5, false, false},
		{"1.0mm미만", false, 0, f(1), 0.5, false, false},
		{"2.0mm", false, 2, f(2), 2, true, false},
		{"0.7", false, 0.7, f(0.7), 0.7, true, false},
		{"30.0~50.0mm", false, 30, f(50), 40, false, false},
		{"30.0 ~ 50.0 mm", false, 30, f(50), 40, false, false},
		{"50.0mm 이상", false, 50, nil, 50, false, false},
		{"1", true, 0, f(3), 1.5, false, false},
		{"2", true, 3, f(15), 9, false, false},
		{"3", true, 15, nil, 15, false, false},
		{"0", true, 0, nil, 0, true, false},
		{"abc", false, 0, nil, 0, false, true},
		{"9", true, 0, nil, 0, false, true},
	}
	for _, c := range cases {
		a, err := ParsePCP(c.in, c.code)
		if c.hasErr {
			if err == nil {
				t.Errorf("%q: want error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		// 코드 0은 Max nil(코드 값만 의미)로 둔다.
		if a.Min != c.min || a.Rep != c.rep || a.Exact != c.exact || (!eqp(a.Max, c.max) && !(c.code && c.in == "0")) {
			t.Errorf("%q: got %+v max=%v", c.in, a, a.Max)
		}
		if c.code && a.Code == nil {
			t.Errorf("%q: code not set", c.in)
		}
	}
}

func TestParseSNO(t *testing.T) {
	a, err := ParseSNO("0.5cm미만", false)
	if err != nil || a.Rep != 0.25 || !eqp(a.Max, f(0.5)) {
		t.Fatalf("got %+v %v", a, err)
	}
	a, err = ParseSNO("1 cm 미만", false)
	if err != nil || a.Rep != 0.5 {
		t.Fatalf("got %+v %v", a, err)
	}
	a, err = ParseSNO("적설없음", false)
	if err != nil || !a.Exact {
		t.Fatalf("got %+v %v", a, err)
	}
	a, err = ParseSNO("2", true)
	if err != nil || a.Min != 1 || a.Max != nil {
		t.Fatalf("got %+v %v", a, err)
	}
}

func TestCodes(t *testing.T) {
	if n, _ := PTY("4"); n != "shower" {
		t.Fatal(n)
	}
	if _, err := PTY("8"); err == nil {
		t.Fatal("want error")
	}
	if IsRain("5") || !IsRain("1") || IsRain("0") {
		t.Fatal("IsRain")
	}
	if n, _ := SKY("3"); n != "partly" {
		t.Fatal(n)
	}
	if lo, hi, _ := WSDCode("2"); lo != 4 || *hi != 9 {
		t.Fatal("wsd")
	}
	for vec, want := range map[float64]string{0: "N", 11.24: "N", 11.25: "NNE", 250: "WSW", 315: "NW", 348: "NNW", 360: "N"} {
		if got := Dir16(vec); got != want {
			t.Errorf("Dir16(%v)=%s want %s", vec, got, want)
		}
	}
}
