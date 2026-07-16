// Package semver provides lightweight semantic version parsing and comparison
// for LadderAirport agent/panel upgrade checks. It is intentionally dependency-free.
package semver

import (
	"strconv"
	"strings"
)

// V is a parsed semantic version (major.minor.patch + optional prerelease).
type V struct {
	Major int
	Minor int
	Patch int
	Pre   string // without leading '-', e.g. "dev", "rc.1"
	Raw   string
	Valid bool
}

// Parse extracts a semver-ish value from strings like "v0.3.1", "0.1.0-dev",
// "1.2", or free-form text containing a version. Returns Valid=false when no
// numeric core can be found.
func Parse(s string) V {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return V{Raw: raw}
	}
	// Prefer the first token that looks like a version (handles "ladder-agent 0.3.1").
	candidate := raw
	for _, part := range strings.Fields(raw) {
		p := strings.Trim(part, ",;()[]{}")
		if looksLikeVersion(p) {
			candidate = p
			break
		}
	}
	candidate = strings.TrimSpace(candidate)
	candidate = strings.TrimPrefix(candidate, "v")
	candidate = strings.TrimPrefix(candidate, "V")

	// Drop build metadata (+git)
	if i := strings.IndexByte(candidate, '+'); i >= 0 {
		candidate = candidate[:i]
	}

	pre := ""
	core := candidate
	if i := strings.IndexByte(candidate, '-'); i >= 0 {
		core = candidate[:i]
		pre = candidate[i+1:]
	}

	segs := strings.Split(core, ".")
	if len(segs) == 0 || segs[0] == "" {
		return V{Raw: raw}
	}
	nums := make([]int, 0, 3)
	for _, seg := range segs {
		if seg == "" {
			return V{Raw: raw}
		}
		// allow only digits in numeric segments
		n, err := strconv.Atoi(seg)
		if err != nil || n < 0 {
			return V{Raw: raw}
		}
		nums = append(nums, n)
		if len(nums) == 3 {
			break
		}
	}
	if len(nums) == 0 {
		return V{Raw: raw}
	}
	for len(nums) < 3 {
		nums = append(nums, 0)
	}
	return V{
		Major: nums[0],
		Minor: nums[1],
		Patch: nums[2],
		Pre:   pre,
		Raw:   raw,
		Valid: true,
	}
}

func looksLikeVersion(s string) bool {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	if s == "" {
		return false
	}
	// must start with a digit
	if s[0] < '0' || s[0] > '9' {
		return false
	}
	return strings.ContainsAny(s, ".+-") || (len(s) > 0 && isAllDigits(s))
}

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Compare returns -1 if a<b, 0 if equal, 1 if a>b.
// Invalid versions compare as less than valid ones; two invalids compare equal.
func Compare(a, b V) int {
	if !a.Valid && !b.Valid {
		return 0
	}
	if !a.Valid {
		return -1
	}
	if !b.Valid {
		return 1
	}
	if a.Major != b.Major {
		return cmpInt(a.Major, b.Major)
	}
	if a.Minor != b.Minor {
		return cmpInt(a.Minor, b.Minor)
	}
	if a.Patch != b.Patch {
		return cmpInt(a.Patch, b.Patch)
	}
	// No prerelease > any prerelease (semver 11.4)
	if a.Pre == "" && b.Pre == "" {
		return 0
	}
	if a.Pre == "" {
		return 1
	}
	if b.Pre == "" {
		return -1
	}
	return comparePre(a.Pre, b.Pre)
}

func cmpInt(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// comparePre compares prerelease identifiers per semver (dot-separated).
func comparePre(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := len(as)
	if len(bs) < n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		ai, aNum := atoiOK(as[i])
		bi, bNum := atoiOK(bs[i])
		switch {
		case aNum && bNum:
			if c := cmpInt(ai, bi); c != 0 {
				return c
			}
		case aNum && !bNum:
			return -1 // numeric < non-numeric
		case !aNum && bNum:
			return 1
		default:
			if as[i] < bs[i] {
				return -1
			}
			if as[i] > bs[i] {
				return 1
			}
		}
	}
	return cmpInt(len(as), len(bs))
}

func atoiOK(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// IsOutdated reports whether current should be upgraded toward recommended.
//
// Rules:
//   - empty recommended → false (nothing to target)
//   - empty / unknown / bare "dev" current → true
//   - both parseable → current < recommended
//   - current unparseable but recommended parseable → true
//   - recommended unparseable → fall back to normalized string inequality
func IsOutdated(current, recommended string) bool {
	recRaw := strings.TrimSpace(recommended)
	if recRaw == "" {
		return false
	}
	curRaw := strings.TrimSpace(current)
	if curRaw == "" {
		return true
	}
	low := strings.ToLower(curRaw)
	if low == "unknown" || low == "dev" {
		return true
	}

	cur := Parse(curRaw)
	rec := Parse(recRaw)

	if cur.Valid && rec.Valid {
		return Compare(cur, rec) < 0
	}
	if rec.Valid && !cur.Valid {
		// e.g. current="0.1.0-dev" still parses; truly free-form → treat outdated
		return true
	}
	// Neither parseable as semver: normalize and compare equality only.
	return normalizeLoose(curRaw) != normalizeLoose(recRaw)
}

func normalizeLoose(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	return strings.ToLower(v)
}
