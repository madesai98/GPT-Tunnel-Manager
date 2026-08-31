package enrichment

import (
	"reflect"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/embedding"
)

func TestToolArtifactIdentityIgnoresNeighborhoodChurn(t *testing.T) {
	self := ToolRef{
		MemberKey:         "srv/tool",
		ServerID:          "srv",
		ToolName:          "tool",
		SourceFingerprint: "sha256:self",
	}
	left := ToolWork{
		Tool:                           self,
		NeighborhoodContextFingerprint: "sha256:left-context",
		Neighbors: []NeighborRef{{
			Rank: 1, MemberKey: "srv/neighbor-a", ServerID: "srv", ToolName: "neighbor-a", SourceFingerprint: "sha256:a",
		}},
	}
	right := ToolWork{
		Tool:                           self,
		NeighborhoodContextFingerprint: "sha256:right-context",
		Neighbors: []NeighborRef{{
			Rank: 1, MemberKey: "srv/neighbor-b", ServerID: "srv", ToolName: "neighbor-b", SourceFingerprint: "sha256:b",
		}},
	}

	leftSpec := toolArtifactSpec(left)
	rightSpec := toolArtifactSpec(right)
	if !reflect.DeepEqual(leftSpec.Dependencies, rightSpec.Dependencies) || leftSpec.ContextFingerprint != rightSpec.ContextFingerprint {
		t.Fatalf("same tool contract produced neighborhood-dependent artifact identity:\nleft=%#v\nright=%#v", leftSpec, rightSpec)
	}
	if leftSpec.ContextFingerprint != "" {
		t.Fatalf("tool enrichment context fingerprint = %q, want stable empty context", leftSpec.ContextFingerprint)
	}

	identity := embedding.Identity{Provider: "local", Model: "test", Protocol: embedding.IdentityVersion}
	leftKey, leftGate := enrichedEmbeddingGate(left, identity)
	rightKey, rightGate := enrichedEmbeddingGate(right, identity)
	if leftKey != rightKey || leftGate != rightGate {
		t.Fatalf("same tool contract produced neighborhood-dependent embedding gate: %s/%s != %s/%s", leftKey, leftGate, rightKey, rightGate)
	}
}
