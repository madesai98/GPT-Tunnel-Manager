package mcpmanager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExactlyFourTools(t *testing.T) {
	xs := tools()
	if len(xs) != 4 {
		t.Fatalf("got %d tools", len(xs))
	}
	want := map[string]bool{"get_status": true, "start": true, "restart": true, "shutdown": true}
	for _, x := range xs {
		name, _ := x["name"].(string)
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("missing %v", want)
	}
}

func TestRejectsBrowserOrigin(t *testing.T) {
	s := New(nil)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	s.handle(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got status %d", rec.Code)
	}
}

func TestStrictArgumentsRejectUnknownFields(t *testing.T) {
	var args struct {
		ServerID string `json:"server_id"`
	}
	if err := decodeStrict([]byte(`{"server_id":"srv_0123456789abcdef0123456789abcdef","unexpected":"value"}`), &args); err == nil {
		t.Fatal("expected unknown field rejection")
	}
}

func TestInitializeIsLegacyAndStateless(t *testing.T) {
	s := New(nil)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	rec := httptest.NewRecorder()
	s.handle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d", rec.Code)
	}
	if got := rec.Header().Get("Mcp-Session-Id"); got != "" {
		t.Fatalf("unexpected session id %q", got)
	}

	var response struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Result.ProtocolVersion != legacyProtocolVersion {
		t.Fatalf("protocolVersion=%q, want %q", response.Result.ProtocolVersion, legacyProtocolVersion)
	}
}

func TestServerDiscoverAdvertisesModernStatelessProtocol(t *testing.T) {
	s := New(nil)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":"discover-1","method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`))
	rec := httptest.NewRecorder()
	s.handle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d", rec.Code)
	}

	var response struct {
		Result struct {
			ResultType        string         `json:"resultType"`
			SupportedVersions []string       `json:"supportedVersions"`
			Capabilities      map[string]any `json:"capabilities"`
			TTLMillis         int            `json:"ttlMs"`
			CacheScope        string         `json:"cacheScope"`
			Meta              map[string]any `json:"_meta"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Result.ResultType != "complete" {
		t.Fatalf("resultType=%q", response.Result.ResultType)
	}
	if len(response.Result.SupportedVersions) != 1 || response.Result.SupportedVersions[0] != modernProtocolVersion {
		t.Fatalf("supportedVersions=%v", response.Result.SupportedVersions)
	}
	if _, ok := response.Result.Capabilities["tools"]; !ok {
		t.Fatalf("missing tools capability: %v", response.Result.Capabilities)
	}
	if response.Result.TTLMillis != discoveryTTLMillis {
		t.Fatalf("ttlMs=%d, want %d", response.Result.TTLMillis, discoveryTTLMillis)
	}
	if response.Result.CacheScope != "public" {
		t.Fatalf("cacheScope=%q", response.Result.CacheScope)
	}
	if _, ok := response.Result.Meta["io.modelcontextprotocol/serverInfo"]; !ok {
		t.Fatalf("missing serverInfo metadata: %v", response.Result.Meta)
	}
}

func TestToolsListIncludesModernCacheFields(t *testing.T) {
	s := New(nil)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`))
	rec := httptest.NewRecorder()
	s.handle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d", rec.Code)
	}
	var response struct {
		Result struct {
			ResultType string           `json:"resultType"`
			Tools      []map[string]any `json:"tools"`
			TTLMillis  int              `json:"ttlMs"`
			CacheScope string           `json:"cacheScope"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Result.ResultType != "complete" || response.Result.CacheScope != "public" || response.Result.TTLMillis != discoveryTTLMillis {
		t.Fatalf("unexpected list metadata: %+v", response.Result)
	}
	if len(response.Result.Tools) != 4 {
		t.Fatalf("got %d tools", len(response.Result.Tools))
	}
}

func TestGetDoesNotPretendToOfferSSE(t *testing.T) {
	s := New(nil)
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/mcp", nil)
	rec := httptest.NewRecorder()
	s.handle(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got status %d", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "POST, OPTIONS, DELETE" {
		t.Fatalf("Allow=%q", got)
	}
}
