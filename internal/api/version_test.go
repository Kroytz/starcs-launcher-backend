package api

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2", "1.2.0", 0},
		{"0.10.0", "0.9.9", 1},
		{"0.9.9", "0.10.0", -1},
		{"1.0.0", "1.0.0", 0},
		{"", "0.1.0", -1},
		{"2.0.0", "10.0.0", -1},
	}
	for _, tc := range cases {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestNormalizeVersion(t *testing.T) {
	cases := map[string]string{
		"v1.0.0-beta": "1.0.0",
		"1.0.0+build": "1.0.0",
		" 0.2.0 ":     "0.2.0",
		"v0.2.0":      "0.2.0",
	}
	for input, want := range cases {
		if got := normalizeVersion(input); got != want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", input, got, want)
		}
	}
	if compareVersions(normalizeVersion("v1.0.0-beta"), normalizeVersion("1.0.0")) != 0 {
		t.Error("v1.0.0-beta should compare equal to 1.0.0 after normalization")
	}
}
