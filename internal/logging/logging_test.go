package logging

import (
	"strings"
	"testing"
	"time"
)

func TestRedactionBeforeRing(t *testing.T) {
	l, err := New(t.TempDir(), "trace", 5, false, "debug", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("sk-test-secret-value")
	l.Redactor().Register(secret)
	l.Log(Info, "Manager", "Test", "token sk-test-secret-value", map[string]any{
		"authorization": "Bearer abc",
		"x":             "sk-test-secret-value",
	})
	b := l.Ring().Snapshot()
	if len(b) != 1 {
		t.Fatal(len(b))
	}
	s := b[0].Message + " " + b[0].Fields["x"].(string)
	if strings.Contains(s, string(secret)) {
		t.Fatal("secret retained")
	}
}

func TestTunnelClientJSONLogIsNormalized(t *testing.T) {
	l, err := New(t.TempDir(), "trace", 5, false, "debug", 1, 1)
	if err != nil {
		t.Fatal(err)
	}

	const line = `{"time":"2026-08-27T03:28:08.2800964-07:00","level":"DEBUG","msg":"poll cycle started","component":"controlplane","client_instance_id":"client-1","tunnel_id":"tunnel-1","limit":20}`
	l.Log(Info, "Manager", "Tunnel Client", line, map[string]any{"stream": "stderr"})

	events := l.Ring().Snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	e := events[0]
	if e.Level != Debug {
		t.Fatalf("expected debug, got %q", e.Level)
	}
	if e.Message != "poll cycle started" {
		t.Fatalf("unexpected message %q", e.Message)
	}
	if e.Component != "Tunnel Client/controlplane" {
		t.Fatalf("unexpected component %q", e.Component)
	}
	wantTime, _ := time.Parse(time.RFC3339Nano, "2026-08-27T03:28:08.2800964-07:00")
	if !e.Timestamp.Equal(wantTime) {
		t.Fatalf("unexpected timestamp %s", e.Timestamp)
	}
	for _, key := range []string{"time", "timestamp", "level", "msg", "message", "component"} {
		if _, exists := e.Fields[key]; exists {
			t.Fatalf("canonical key %q was retained in fields", key)
		}
	}
	if e.Fields["client_instance_id"] != "client-1" || e.Fields["tunnel_id"] != "tunnel-1" || e.Fields["stream"] != "stderr" {
		t.Fatalf("structured fields were not preserved: %#v", e.Fields)
	}
}

func TestTunnelClientJSONLevelControlsCapture(t *testing.T) {
	l, err := New(t.TempDir(), "info", 5, false, "debug", 1, 1)
	if err != nil {
		t.Fatal(err)
	}

	l.Log(Info, "Manager", "Tunnel Client", `{"level":"DEBUG","msg":"debug child log"}`, nil)
	if got := len(l.Ring().Snapshot()); got != 0 {
		t.Fatalf("debug child log bypassed info capture threshold: %d events", got)
	}

	l.Log(Info, "Manager", "Tunnel Client", `{"level":"WARN","msg":"warning child log"}`, nil)
	events := l.Ring().Snapshot()
	if len(events) != 1 || events[0].Level != Warn {
		t.Fatalf("warning level was not preserved: %#v", events)
	}
}

func TestTunnelClientInfoIsReclassifiedByOperatorRelevance(t *testing.T) {
	l, err := New(t.TempDir(), "trace", 5, false, "debug", 1, 1)
	if err != nil {
		t.Fatal(err)
	}

	l.Log(Info, "Manager", "Tunnel Client", `{"level":"INFO","msg":"provided","type":"*config.Config","stacktrace":{"0":"fx.New"},"moduletrace":{"0":"app.New"}}`, nil)
	l.Log(Info, "Manager", "Tunnel Client", `{"level":"INFO","msg":"control-plane route resolved","component":"controlplane"}`, nil)
	l.Log(Info, "Manager", "Tunnel Client", `{"level":"INFO","msg":"🟢 tunnel-client started"}`, nil)

	events := l.Ring().Snapshot()
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Level != Trace {
		t.Fatalf("Fx graph record should be trace, got %q", events[0].Level)
	}
	if events[1].Level != Debug {
		t.Fatalf("runtime plumbing should be debug, got %q", events[1].Level)
	}
	if events[2].Level != Info {
		t.Fatalf("runtime readiness should remain info, got %q", events[2].Level)
	}
}

func TestSemanticTunnelClientLevelsRespectCapture(t *testing.T) {
	l, err := New(t.TempDir(), "info", 5, false, "debug", 1, 1)
	if err != nil {
		t.Fatal(err)
	}

	l.Log(Info, "Manager", "Tunnel Client", `{"level":"INFO","msg":"run","stacktrace":{"0":"fx"},"moduletrace":{"0":"fx"}}`, nil)
	l.Log(Info, "Manager", "Tunnel Client", `{"level":"INFO","msg":"poller started","component":"controlplane"}`, nil)
	l.Log(Info, "Manager", "Tunnel Client", `{"level":"INFO","msg":"🟢 tunnel-client started"}`, nil)

	events := l.Ring().Snapshot()
	if len(events) != 1 || events[0].Level != Info {
		t.Fatalf("info capture should retain only high-level info: %#v", events)
	}
}

func TestLifecycleLevelsAreOperatorFacing(t *testing.T) {
	l, err := New(t.TempDir(), "trace", 5, false, "debug", 1, 1)
	if err != nil {
		t.Fatal(err)
	}

	l.Log(Info, "Manager", "Lifecycle", "managed_activity_observed", nil)
	l.Log(Info, "Manager", "Lifecycle", "server_starting", nil)
	l.Log(Info, "Manager", "Lifecycle", "tunnel_ready", nil)
	l.Log(Info, "Manager", "Lifecycle", "tunnel_disconnected", nil)
	l.Log(Info, "Manager", "Lifecycle", "server_crashed", nil)

	events := l.Ring().Snapshot()
	want := []Level{Trace, Debug, Info, Warn, Error}
	if len(events) != len(want) {
		t.Fatalf("expected %d events, got %d", len(want), len(events))
	}
	for i, level := range want {
		if events[i].Level != level {
			t.Fatalf("event %d: expected %q, got %q", i, level, events[i].Level)
		}
	}
}

func TestStructuredFieldsAreRecursivelyRedacted(t *testing.T) {
	l, err := New(t.TempDir(), "trace", 5, false, "debug", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("nested-secret-value")
	l.Redactor().Register(secret)

	l.Log(Info, "Manager", "Tunnel Client", `{"level":"INFO","msg":"structured","stacktrace":{"frame":"nested-secret-value"},"values":["nested-secret-value"]}`, nil)
	e := l.Ring().Snapshot()[0]
	frame := e.Fields["stacktrace"].(map[string]any)["frame"].(string)
	value := e.Fields["values"].([]any)[0].(string)
	if strings.Contains(frame, string(secret)) || strings.Contains(value, string(secret)) {
		t.Fatal("nested structured fields retained a registered secret")
	}
}
