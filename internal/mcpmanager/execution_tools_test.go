package mcpmanager

import (
	"encoding/json"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/executionrouter"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestPhase8ExecutorDefinitionsCoverAllTenExactClasses(t *testing.T) {
	want := map[toolcontract.ExecutorClass]bool{
		toolcontract.ExecutorReadOnlyClosed:              true,
		toolcontract.ExecutorReadOnlyOpen:                true,
		toolcontract.ExecutorAdditiveClosed:              true,
		toolcontract.ExecutorAdditiveClosedIdempotent:    true,
		toolcontract.ExecutorAdditiveOpen:                true,
		toolcontract.ExecutorAdditiveOpenIdempotent:      true,
		toolcontract.ExecutorDestructiveClosed:           true,
		toolcontract.ExecutorDestructiveClosedIdempotent: true,
		toolcontract.ExecutorDestructiveOpen:             true,
		toolcontract.ExecutorDestructiveOpenIdempotent:   true,
	}
	if len(executorDefinitions) != len(want) {
		t.Fatalf("executor definitions = %d, want %d", len(executorDefinitions), len(want))
	}
	for _, definition := range executorDefinitions {
		if !want[definition.Class] {
			t.Fatalf("unexpected executor class %q", definition.Class)
		}
		openWorld := definition.OpenWorld
		var destructiveHint *bool
		if !definition.ReadOnly {
			destructive := definition.Destructive
			destructiveHint = &destructive
		}
		tool := &mcp.Tool{
			Name: string(definition.Class),
			Annotations: &mcp.ToolAnnotations{
				ReadOnlyHint:    definition.ReadOnly,
				DestructiveHint: destructiveHint,
				IdempotentHint:  definition.Idempotent,
				OpenWorldHint:   &openWorld,
			},
		}
		normalized, err := toolcontract.ExecutorClassForTool(tool)
		if err != nil {
			t.Fatalf("normalize %q annotations: %v", definition.Class, err)
		}
		if normalized != definition.Class {
			t.Fatalf("executor %q annotations normalize to %q", definition.Class, normalized)
		}
		delete(want, definition.Class)
	}
	if len(want) != 0 {
		t.Fatalf("missing executor classes: %v", want)
	}
}

func TestPhase8UnavailableRouterReturnsOutcomeAwareToolError(t *testing.T) {
	result := executionFailureResult(&executionrouter.ExecutionError{
		Code:      executionrouter.CodeManagerUnavailable,
		Message:   "manager execution router is unavailable",
		Outcome:   executionrouter.OutcomeNotStarted,
		Retryable: true,
	})
	if !result.IsError {
		t.Fatal("unavailable execution service must be a tool error")
	}
	encoded, ok := result.StructuredContent.(json.RawMessage)
	if !ok {
		t.Fatalf("structured error type = %T", result.StructuredContent)
	}
	var structured map[string]any
	if err := json.Unmarshal(encoded, &structured); err != nil {
		t.Fatal(err)
	}
	errorBody, ok := structured["error"].(map[string]any)
	if !ok || errorBody["code"] != "manager_unavailable" || errorBody["outcome"] != "not_started" || errorBody["retryable"] != true {
		t.Fatalf("structured error = %#v", structured)
	}
}
