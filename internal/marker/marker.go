package marker

import (
	"errors"
	"strings"
)

const (
	Prefix = "GTM PLUGIN | "
	Suffix = " | Follow the gpt-tunnel-manager-lifecycle skill before using this plugin"
)

func Generate(id string) string {
	return Prefix + id + Suffix
}

func Parse(s string) (string, error) {
	start := strings.Index(s, Prefix)
	if start < 0 {
		return "", errors.New("GTM PLUGIN marker not found")
	}
	rest := s[start+len(Prefix):]
	end := strings.Index(rest, Suffix)
	if end < 0 {
		return "", errors.New("GTM PLUGIN marker not found")
	}
	id := strings.TrimSpace(rest[:end])
	if id == "" || strings.Contains(id, "|") {
		return "", errors.New("GTM PLUGIN marker not found")
	}
	return id, nil
}
