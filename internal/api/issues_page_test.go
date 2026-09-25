package api

import "testing"

func TestParseListPage(t *testing.T) {
	for _, tt := range []struct {
		raw string
		max int
		want int
		valid bool
	}{
		{"", 1000, 0, true},
		{"0", 1000, 0, true},
		{"1000", 1000, 1000, true},
		{"1001", 1000, 0, false},
		{"-1", 1000, 0, false},
		{"abc", 1000, 0, false},
	} {
		got, err := parseListPage(tt.raw, "limit", tt.max)
		if got != tt.want || (err == nil) != tt.valid {
			t.Errorf("parseListPage(%q) = %d, %v", tt.raw, got, err)
		}
	}
}
