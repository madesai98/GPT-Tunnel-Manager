package config

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func strictRoundTrip(t *testing.T, value TransportConfig) TransportConfig {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var decoded TransportConfig
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(value, decoded) {
		t.Fatalf("round trip mismatch\nwant: %#v\ngot:  %#v", value, decoded)
	}
	return decoded
}

func TestTransportJSONRoundTrips(t *testing.T) {
	cases := map[string]TransportConfig{
		"stdio": {
			Type: TransportStdio,
			Stdio: &StdioTransport{
				Executable:       "node",
				Args:             []string{"server.js", "--stdio"},
				WorkingDirectory: "/srv/example",
			},
		},
		"managed_http": {
			Type: TransportManagedHTTP,
			ManagedHTTP: &ManagedHTTPTransport{
				URL: "http://127.0.0.1:4000/mcp",
				Launch: LaunchConfig{
					Executable:       "example-server",
					Args:             []string{"serve"},
					WorkingDirectory: "/srv/example",
				},
			},
		},
		"external_http": {
			Type:         TransportExternalHTTP,
			ExternalHTTP: &ExternalHTTPTransport{URL: "https://example.test/mcp"},
		},
	}
	for name, transport := range cases {
		t.Run(name, func(t *testing.T) { strictRoundTrip(t, transport) })
	}
}

func TestManagedHTTPUsesCanonicalJSONFieldOnly(t *testing.T) {
	value := TransportConfig{
		Type: TransportManagedHTTP,
		ManagedHTTP: &ManagedHTTPTransport{
			URL:    "http://127.0.0.1:4000/mcp",
			Launch: LaunchConfig{Executable: "server"},
		},
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"managed_http"`) {
		t.Fatalf("canonical managed_http field missing: %s", text)
	}
	if strings.Contains(text, "manaed_http") {
		t.Fatalf("legacy typo was serialized: %s", text)
	}
}

func TestLegacyManagedHTTPTypoIsRejected(t *testing.T) {
	raw := []byte(`{"type":"managed_http","manaed_http":{"url":"http://127.0.0.1:4000/mcp","launch":{"executable":"server"}}}`)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var transport TransportConfig
	if err := decoder.Decode(&transport); err == nil {
		t.Fatal("legacy manaed_http typo was accepted")
	}
}

func TestManagerStrictEnumValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ManagerConfig)
	}{
		{"capture level", func(c *ManagerConfig) { c.Logging.CaptureLevel = "verbose" }},
		{"display level", func(c *ManagerConfig) { c.Logging.DisplayLevel = "notice" }},
		{"disk minimum level", func(c *ManagerConfig) { c.Logging.DiskMinimumLevel = "critical" }},
		{"channel", func(c *ManagerConfig) { c.TunnelClient.Channel = "nightly" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultManagerConfig()
			tc.mutate(&cfg)
			if err := ValidateManager(cfg); err == nil {
				t.Fatal("expected strict enum validation error")
			}
		})
	}
}
