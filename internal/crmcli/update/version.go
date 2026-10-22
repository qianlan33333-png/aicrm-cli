// Package cliupdate reads public release metadata and updates only the local CLI.
// It never accepts business credentials or executes business/provider operations.
package cliupdate

import (
	"errors"
	"strings"
)

type version struct {
	core [3]string
	pre  []string
}

func parseVersion(s string) (version, error) {
	v := version{}
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		if !identifiers(s[i+1:], false) {
			return v, errors.New("invalid semantic version")
		}
		s = s[:i]
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		if !identifiers(s[i+1:], true) {
			return v, errors.New("invalid semantic version")
		}
		v.pre = strings.Split(s[i+1:], ".")
		s = s[:i]
	}
	p := strings.Split(s, ".")
	if len(p) != 3 {
		return v, errors.New("invalid semantic version")
	}
	for i, n := range p {
		if !digits(n) || len(n) > 1 && n[0] == '0' {
			return v, errors.New("invalid semantic version")
		}
		v.core[i] = n
	}
	return v, nil
}
func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func identifiers(s string, prerelease bool) bool {
	for _, p := range strings.Split(s, ".") {
		if p == "" || prerelease && digits(p) && len(p) > 1 && p[0] == '0' {
			return false
		}
		for _, c := range p {
			if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '-') {
				return false
			}
		}
	}
	return true
}
func numericCompare(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

// Compare implements SemVer precedence, including arbitrarily long numeric
// components; build metadata never changes precedence.
func Compare(a, b string) (int, error) {
	x, e := parseVersion(a)
	if e != nil {
		return 0, e
	}
	y, e := parseVersion(b)
	if e != nil {
		return 0, e
	}
	for i := range x.core {
		if c := numericCompare(x.core[i], y.core[i]); c != 0 {
			return c, nil
		}
	}
	if len(x.pre) == 0 && len(y.pre) == 0 {
		return 0, nil
	}
	if len(x.pre) == 0 {
		return 1, nil
	}
	if len(y.pre) == 0 {
		return -1, nil
	}
	for i := 0; i < len(x.pre) && i < len(y.pre); i++ {
		a, b := x.pre[i], y.pre[i]
		c := 0
		switch {
		case digits(a) && digits(b):
			c = numericCompare(a, b)
		case digits(a):
			c = -1
		case digits(b):
			c = 1
		default:
			c = strings.Compare(a, b)
		}
		if c != 0 {
			return c, nil
		}
	}
	if len(x.pre) < len(y.pre) {
		return -1, nil
	}
	if len(x.pre) > len(y.pre) {
		return 1, nil
	}
	return 0, nil
}
