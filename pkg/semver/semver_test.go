package semver

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in            string
		maj, min, pat int
		pre           string
		valid         bool
	}{
		{"v0.3.1", 0, 3, 1, "", true},
		{"0.1.0-dev", 0, 1, 0, "dev", true},
		{"1.2", 1, 2, 0, "", true},
		{"v1.2.3-rc.1+build", 1, 2, 3, "rc.1", true},
		{"", 0, 0, 0, "", false},
		{"unknown", 0, 0, 0, "", false},
		{"ladder 0.4.0", 0, 4, 0, "", true},
	}
	for _, c := range cases {
		v := Parse(c.in)
		if v.Valid != c.valid {
			t.Fatalf("Parse(%q).Valid=%v want %v", c.in, v.Valid, c.valid)
		}
		if !c.valid {
			continue
		}
		if v.Major != c.maj || v.Minor != c.min || v.Patch != c.pat || v.Pre != c.pre {
			t.Fatalf("Parse(%q)=%+v want %d.%d.%d pre=%q", c.in, v, c.maj, c.min, c.pat, c.pre)
		}
	}
}

func TestCompare(t *testing.T) {
	less := [][2]string{
		{"0.2.0", "0.3.1"},
		{"v0.3.1-rc.1", "v0.3.1"},
		{"0.1.0-dev", "0.1.0"},
		{"1.0.0-alpha", "1.0.0-alpha.1"},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta"},
		{"1.0.0-beta", "1.0.0"},
	}
	for _, pair := range less {
		a, b := Parse(pair[0]), Parse(pair[1])
		if Compare(a, b) >= 0 {
			t.Fatalf("Compare(%q,%q)=%d want <0", pair[0], pair[1], Compare(a, b))
		}
		if Compare(b, a) <= 0 {
			t.Fatalf("Compare(%q,%q)=%d want >0", pair[1], pair[0], Compare(b, a))
		}
	}
	if Compare(Parse("v0.3.1"), Parse("0.3.1")) != 0 {
		t.Fatal("v prefix should not matter")
	}
}

func TestIsOutdated(t *testing.T) {
	cases := []struct {
		cur, rec string
		want     bool
	}{
		{"", "v0.3.1", true},
		{"0.1.0-dev", "v0.3.1", true},
		{"v0.3.1", "v0.3.1", false},
		{"0.3.1", "v0.3.1", false},
		{"v0.2.0", "v0.3.1", true},
		{"v0.4.0", "v0.3.1", false}, // newer than recommended → not outdated
		{"v0.3.1", "", false},
		{"unknown", "v1.0.0", true},
		{"v0.3.1-rc.1", "v0.3.1", true},
		{"v0.3.1", "v0.3.1-rc.1", false},
		{"dev", "v0.1.0", true},
	}
	for _, c := range cases {
		got := IsOutdated(c.cur, c.rec)
		if got != c.want {
			t.Fatalf("IsOutdated(%q,%q)=%v want %v", c.cur, c.rec, got, c.want)
		}
	}
}
