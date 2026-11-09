package kst

import (
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, Loc)
	if err != nil {
		panic(err)
	}
	return t
}

func TestLatest(t *testing.T) {
	cases := []struct {
		k    Kind
		now  string
		want string
	}{
		{Ncst, "2026-11-10 00:05", "2026-11-09 23:00"},
		{Ncst, "2026-11-10 00:10", "2026-11-10 00:00"},
		{Ultra, "2026-11-10 00:40", "2026-11-09 23:30"},
		{Ultra, "2026-11-10 00:45", "2026-11-10 00:30"},
		{Ultra, "2026-11-10 01:20", "2026-11-10 00:30"},
		{Vilage, "2026-11-10 00:05", "2026-11-09 23:00"},
		{Vilage, "2026-11-10 02:09", "2026-11-09 23:00"},
		{Vilage, "2026-11-10 02:10", "2026-11-10 02:00"},
		{Vilage, "2026-11-10 13:59", "2026-11-10 11:00"},
		{Vilage, "2026-11-10 23:30", "2026-11-10 23:00"},
	}
	for _, c := range cases {
		if got := c.k.Latest(at(c.now)); !got.Equal(at(c.want)) {
			t.Errorf("kind %d now %s: got %s want %s", c.k, c.now, got.Format("2006-01-02 15:04"), c.want)
		}
	}
}

func TestIntRoundTrip(t *testing.T) {
	tm := at("2026-11-09 20:30")
	if v := ToInt(tm); v != 202611092030 {
		t.Fatalf("ToInt=%d", v)
	}
	if !FromInt(202611092030).Equal(tm) {
		t.Fatal("FromInt mismatch")
	}
	// UTC로 표현된 시각도 KST로 바꿔 계산한다.
	if v := Day(time.Date(2026, 11, 9, 15, 30, 0, 0, time.UTC)); v != 20261110 {
		t.Fatalf("Day=%d", v)
	}
	if v, err := ParseBase("20261109", "2000"); err != nil || v != 202611092000 {
		t.Fatalf("ParseBase=%d %v", v, err)
	}
	if _, err := ParseBase("2026110", "2000"); err == nil {
		t.Fatal("want error")
	}
}

func TestNextPrev(t *testing.T) {
	b := at("2026-11-09 23:00")
	if !Vilage.Next(b).Equal(at("2026-11-10 02:00")) || !Vilage.Prev(b).Equal(at("2026-11-09 20:00")) {
		t.Fatal("vilage next/prev")
	}
	if !Ultra.FirstFetch(at("2026-11-09 23:30")).Equal(at("2026-11-09 23:45")) {
		t.Fatal("ultra first fetch")
	}
}
