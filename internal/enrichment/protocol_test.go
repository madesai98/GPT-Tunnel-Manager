package enrichment

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
)

func TestProtocolDescriptorsAreSelfDescribing(t *testing.T) {
	tests := []struct {
		kind     catalog.EnrichmentBatchKind
		protocol string
	}{
		{catalog.BatchToolEnrichment, ToolEnrichmentProtocolVersion},
		{catalog.BatchCapabilityReconciliation, CapabilityProtocolVersion},
		{catalog.BatchAmbiguityReview, AmbiguityReviewProtocolVersion},
	}
	for _, test := range tests {
		descriptor, err := ProtocolDescriptorForBatchKind(test.kind)
		if err != nil {
			t.Fatalf("descriptor for %s: %v", test.kind, err)
		}
		if descriptor.Protocol != test.protocol {
			t.Fatalf("descriptor protocol for %s = %q, want %q", test.kind, descriptor.Protocol, test.protocol)
		}
		if descriptor.ResponseSchema == nil {
			t.Fatalf("descriptor for %s has no response schema", test.kind)
		}
		if len(descriptor.AgentInstructions) == 0 {
			t.Fatalf("descriptor for %s has no agent instructions", test.kind)
		}
		if _, err := json.Marshal(descriptor.ResponseSchema); err != nil {
			t.Fatalf("marshal schema for %s: %v", test.kind, err)
		}
	}
}

func TestCapabilityProtocolDocumentsToolMembersExactly(t *testing.T) {
	descriptor, err := ProtocolDescriptorForBatchKind(catalog.BatchCapabilityReconciliation)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(descriptor.ResponseSchema)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, `"tool_members"`) {
		t.Fatalf("capability schema does not document tool_members: %s", text)
	}
	if strings.Contains(text, `"tools"`) {
		t.Fatalf("capability schema exposes guessed tools field: %s", text)
	}
}

func TestCapabilityResponseRejectsUnknownToolsField(t *testing.T) {
	body := []byte(`{
		"hierarchy": {
			"protocol": "capability-reconciliation/v1",
			"capabilities": [{"id":"search", "name":"Search", "tools":["srv/tool"]}]
		}
	}`)
	var response CapabilityBatchResponse
	err := json.Unmarshal(body, &response)
	if err == nil {
		t.Fatal("expected unknown tools field to be rejected")
	}
	if !strings.Contains(err.Error(), `unknown field "tools"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProtocolResponsesRejectNestedUnknownFields(t *testing.T) {
	body := []byte(`{
		"items": [{
			"member_key": "srv/tool",
			"guidance": {"purpose":"Searches things", "use_when_typo":["search"]}
		}]
	}`)
	var response ToolBatchResponse
	err := json.Unmarshal(body, &response)
	if err == nil {
		t.Fatal("expected nested unknown guidance field to be rejected")
	}
	if !strings.Contains(err.Error(), `unknown field "use_when_typo"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidCapabilityResponseStillDecodes(t *testing.T) {
	body := []byte(`{
		"hierarchy": {
			"protocol": "capability-reconciliation/v1",
			"capabilities": [{"id":"search", "name":"Search", "tool_members":["srv/tool"]}]
		}
	}`)
	var response CapabilityBatchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("valid response rejected: %v", err)
	}
	if len(response.Hierarchy.Capabilities) != 1 || len(response.Hierarchy.Capabilities[0].ToolMembers) != 1 {
		t.Fatalf("decoded hierarchy = %#v", response.Hierarchy)
	}
}
