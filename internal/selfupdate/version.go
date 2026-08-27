package selfupdate

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type semVersion struct {
	major uint64
	minor uint64
	patch uint64
	pre   []string
}

func parseVersion(raw string) (semVersion, error) {
	value := strings.TrimSpace(raw)
	value = strings.TrimPrefix(value, "v")
	if plus := strings.IndexByte(value, '+'); plus >= 0 {
		value = value[:plus]
	}
	var pre []string
	if dash := strings.IndexByte(value, '-'); dash >= 0 {
		preValue := value[dash+1:]
		value = value[:dash]
		if preValue == "" {
			return semVersion{}, errors.New("empty prerelease identifier")
		}
		pre = strings.Split(preValue, ".")
		for _, identifier := range pre {
			if identifier == "" {
				return semVersion{}, errors.New("empty prerelease identifier")
			}
		}
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return semVersion{}, errors.New("version must contain major.minor.patch")
	}
	numbers := make([]uint64, 3)
	for i, part := range parts {
		if part == "" {
			return semVersion{}, errors.New("empty numeric version component")
		}
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return semVersion{}, fmt.Errorf("invalid numeric version component %q", part)
		}
		numbers[i] = n
	}
	return semVersion{major: numbers[0], minor: numbers[1], patch: numbers[2], pre: pre}, nil
}

func (v semVersion) String() string {
	value := fmt.Sprintf("v%d.%d.%d", v.major, v.minor, v.patch)
	if len(v.pre) != 0 {
		value += "-" + strings.Join(v.pre, ".")
	}
	return value
}

func compareVersions(a, b semVersion) int {
	for _, pair := range [][2]uint64{{a.major, b.major}, {a.minor, b.minor}, {a.patch, b.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(a.pre) == 0 && len(b.pre) == 0 {
		return 0
	}
	if len(a.pre) == 0 {
		return 1
	}
	if len(b.pre) == 0 {
		return -1
	}
	limit := len(a.pre)
	if len(b.pre) < limit {
		limit = len(b.pre)
	}
	for i := 0; i < limit; i++ {
		ai, aNumeric := numericIdentifier(a.pre[i])
		bi, bNumeric := numericIdentifier(b.pre[i])
		switch {
		case aNumeric && bNumeric:
			if ai < bi {
				return -1
			}
			if ai > bi {
				return 1
			}
		case aNumeric:
			return -1
		case bNumeric:
			return 1
		default:
			if a.pre[i] < b.pre[i] {
				return -1
			}
			if a.pre[i] > b.pre[i] {
				return 1
			}
		}
	}
	if len(a.pre) < len(b.pre) {
		return -1
	}
	if len(a.pre) > len(b.pre) {
		return 1
	}
	return 0
}

func numericIdentifier(value string) (uint64, bool) {
	if value == "" {
		return 0, false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(value, 10, 64)
	return n, err == nil
}
