package cliupdate

import "testing"

func TestSemVerPrecedenceAndValidation(t *testing.T) {
	ordered := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.10.0", "2.0.0", "999999999999999999999999.0.0"}
	for i := 1; i < len(ordered); i++ {
		c, e := Compare(ordered[i-1], ordered[i])
		if e != nil || c >= 0 {
			t.Fatalf("precedence %s %s: %d %v", ordered[i-1], ordered[i], c, e)
		}
	}
	for _, p := range [][2]string{{"v1.2.3", "1.2.3+build.19"}, {"1.0.0+foo", "1.0.0+bar"}} {
		c, e := Compare(p[0], p[1])
		if e != nil || c != 0 {
			t.Fatalf("build metadata changes precedence: %v %d %v", p, c, e)
		}
	}
	for _, s := range []string{"", "dev", "1.2", "01.0.0", "1.00.0", "1.0.0-01", "1.0.0-", "1.0.0+", "1.0.0+x+y", "1.0.0-foo..bar", "v1.0.0\n", "1.0.0/../../x"} {
		if _, e := parseVersion(s); e == nil {
			t.Errorf("accepted invalid version %q", s)
		}
	}
}
func FuzzSemVerComparison(f *testing.F) {
	for _, p := range [][2]string{{"1.0.0", "1.0.1"}, {"v1.2.3-rc.1", "1.2.3"}, {"dev", ""}, {"999999999999999999.0.0", "9.0.0"}} {
		f.Add(p[0], p[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		x, e := Compare(a, b)
		if e != nil {
			return
		}
		y, e := Compare(b, a)
		if e != nil || x != -y {
			t.Fatalf("comparison not antisymmetric: %q %q", a, b)
		}
		z, e := Compare(a, a)
		if e != nil || z != 0 {
			t.Fatal("version not equal to itself")
		}
	})
}
