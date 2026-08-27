package buildinfo

import "strings"

// Version is injected by the release workflow. Development builds deliberately
// identify themselves as dev so they cannot be mistaken for a published build.
var Version = "dev"

func DisplayVersion() string {
	value := strings.TrimSpace(Version)
	if value == "" {
		return "dev"
	}
	if value == "dev" || strings.HasPrefix(value, "v") {
		return value
	}
	return "v" + value
}

func IsRelease() bool {
	value := strings.TrimPrefix(strings.TrimSpace(Version), "v")
	parts := strings.SplitN(value, "-", 2)
	numeric := strings.Split(parts[0], ".")
	if len(numeric) != 3 {
		return false
	}
	for _, part := range numeric {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}
