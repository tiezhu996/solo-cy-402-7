package util

import (
	"encoding/json"
	"testing"
)

func TestParseYuanToCents(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int64
		ok   bool
	}{
		{"integer yuan", "100", 10000, true},
		{"two decimals", "12.34", 1234, true},
		{"one decimal pads zero", "12.3", 1230, true},
		{"zero", "0", 0, true},
		{"large", "1000000.00", 100000000, true},
		{"leading/trailing spaces", "  5.20 ", 520, true},
		{"three decimals rejected", "12.345", 0, false},
		{"letters rejected", "abc", 0, false},
		{"negative rejected", "-1", 0, false},
		{"scientific notation rejected", "1e3", 0, false},
		{"empty rejected", "", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseYuanToCents(json.Number(tc.in))
			if tc.ok != (err == nil) {
				t.Fatalf("in=%q err=%v, want ok=%v", tc.in, err, tc.ok)
			}
			if tc.ok && got != tc.want {
				t.Fatalf("in=%q got=%d want=%d", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatCents(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0.00"},
		{5, "0.05"},
		{1234, "12.34"},
		{123456, "1,234.56"},
		{123456000, "1,234,560.00"},
		{-520, "-5.20"},
	}
	for _, tc := range cases {
		if got := FormatCents(tc.in); got != tc.want {
			t.Fatalf("FormatCents(%d)=%q want %q", tc.in, got, tc.want)
		}
	}
}
