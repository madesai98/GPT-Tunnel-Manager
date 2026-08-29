package enrichment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/embedding"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/retrieval"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
)

func toolRef(source catalog.SourceToolRecord) ToolRef {
	return ToolRef{
		MemberKey:         MemberKey(source.ServerID, source.ToolName),
		ServerID:          source.ServerID,
		ToolName:          source.ToolName,
		SourceFingerprint: source.SourceFingerprint,
		Contract:          append(json.RawMessage(nil), source.ContractJSON...),
	}
}

func neighborhoodFingerprint(work ToolWork) (string, error) {
	body, err := json.Marshal(struct {
		Algorithm string `json:"algorithm"`
		Self      struct {
			MemberKey   string `json:"member_key"`
			Fingerprint string `json:"fingerprint"`
		} `json:"self"`
		Neighbors []struct {
			Rank        int    `json:"rank"`
			MemberKey   string `json:"member_key"`
			Fingerprint string `json:"fingerprint"`
		} `json:"neighbors"`
	}{
		Algorithm: NeighborhoodAlgorithmVersion,
		Self: struct {
			MemberKey   string `json:"member_key"`
			Fingerprint string `json:"fingerprint"`
		}{work.Tool.MemberKey, work.Tool.SourceFingerprint},
		Neighbors: func() []struct {
			Rank        int    `json:"rank"`
			MemberKey   string `json:"member_key"`
			Fingerprint string `json:"fingerprint"`
		} {
			result := make([]struct {
				Rank        int    `json:"rank"`
				MemberKey   string `json:"member_key"`
				Fingerprint string `json:"fingerprint"`
			}, len(work.Neighbors))
			for index, neighbor := range work.Neighbors {
				result[index] = struct {
					Rank        int    `json:"rank"`
					MemberKey   string `json:"member_key"`
					Fingerprint string `json:"fingerprint"`
				}{neighbor.Rank, neighbor.MemberKey, neighbor.SourceFingerprint}
			}
			return result
		}(),
	})
	if err != nil {
		return "", err
	}
	return toolcontract.FingerprintJSON(body), nil
}

func toolArtifactSpec(work ToolWork) catalog.RequiredArtifactSpec {
	dependencies := []catalog.ArtifactDependency{
		{Key: "enrichment.protocol", Fingerprint: fingerprintText(ToolEnrichmentProtocolVersion)},
		{Key: "source.self", Fingerprint: work.Tool.SourceFingerprint},
	}
	for _, neighbor := range work.Neighbors {
		dependencies = append(dependencies, catalog.ArtifactDependency{Key: "source.neighbor:" + neighbor.MemberKey, Fingerprint: neighbor.SourceFingerprint})
	}
	return catalog.RequiredArtifactSpec{
		Role:               RoleToolEnrichment,
		MemberKey:          work.Tool.MemberKey,
		Kind:               ToolEnrichmentArtifactKind,
		Dependencies:       dependencies,
		ContextFingerprint: work.NeighborhoodContextFingerprint,
	}
}

func enrichedEmbeddingGate(work ToolWork, identity embedding.Identity) (string, string) {
	key := "enriched-embedding:" + work.Tool.MemberKey
	body, _ := json.Marshal(struct {
		Projection string `json:"projection"`
		Provider   string `json:"provider"`
		Source     string `json:"source"`
		Context    string `json:"context"`
	}{EnrichedProjectionVersion, identity.Fingerprint(), work.Tool.SourceFingerprint, work.NeighborhoodContextFingerprint})
	return key, toolcontract.FingerprintJSON(body)
}

func enrichedProjection(work ToolWork, guidancePayload []byte) (retrieval.Projection, error) {
	var guidance ToolGuidance
	if err := json.Unmarshal(guidancePayload, &guidance); err != nil {
		return retrieval.Projection{}, fmt.Errorf("decode tool guidance for enriched embedding: %w", err)
	}
	body, err := json.Marshal(struct {
		MemberKey string       `json:"member_key"`
		ToolName  string       `json:"tool_name"`
		Guidance  ToolGuidance `json:"guidance"`
	}{work.Tool.MemberKey, work.Tool.ToolName, guidance})
	if err != nil {
		return retrieval.Projection{}, err
	}
	text := string(body)
	return retrieval.Projection{Version: EnrichedProjectionVersion, Text: text, Fingerprint: projectionFingerprint(EnrichedProjectionVersion, text)}, nil
}

func validateHierarchy(hierarchy CapabilityHierarchy, known map[string]struct{}) error {
	if hierarchy.Protocol != CapabilityProtocolVersion {
		return fmt.Errorf("capability hierarchy protocol %q is unsupported", hierarchy.Protocol)
	}
	if len(hierarchy.Capabilities) == 0 && len(known) != 0 {
		return errors.New("capability hierarchy is empty")
	}
	nodes := make(map[string]CapabilityNode, len(hierarchy.Capabilities))
	assigned := make(map[string]struct{}, len(known))
	for _, node := range hierarchy.Capabilities {
		node.ID = strings.TrimSpace(node.ID)
		node.Name = strings.TrimSpace(node.Name)
		if node.ID == "" || node.Name == "" {
			return errors.New("capability nodes require id and name")
		}
		if _, exists := nodes[node.ID]; exists {
			return fmt.Errorf("duplicate capability id %q", node.ID)
		}
		nodes[node.ID] = node
		for _, member := range node.ToolMembers {
			if _, ok := known[member]; !ok {
				return fmt.Errorf("capability %s references unknown tool %q", node.ID, member)
			}
			assigned[member] = struct{}{}
		}
	}
	for _, node := range nodes {
		if node.ParentID != "" {
			if _, ok := nodes[node.ParentID]; !ok {
				return fmt.Errorf("capability %s references missing parent %q", node.ID, node.ParentID)
			}
		}
		seen := map[string]struct{}{node.ID: {}}
		parent := node.ParentID
		for parent != "" {
			if _, cycle := seen[parent]; cycle {
				return fmt.Errorf("capability hierarchy contains a cycle at %q", parent)
			}
			seen[parent] = struct{}{}
			parent = nodes[parent].ParentID
		}
	}
	for member := range known {
		if _, ok := assigned[member]; !ok {
			return fmt.Errorf("tool %s is not assigned to any reconciled capability", member)
		}
	}
	return nil
}

func validateAmbiguity(proposal AmbiguityProposal, known map[string]struct{}) error {
	if strings.TrimSpace(proposal.Summary) == "" {
		return errors.New("ambiguity review requires a source-grounded summary")
	}
	if len(proposal.CompetingTools) < 2 {
		return errors.New("ambiguity review requires at least two competing tools")
	}
	if len(proposal.ConditionalUseCases) == 0 {
		return errors.New("ambiguity review requires conditional use cases")
	}
	if len(proposal.SuggestedOptions) == 0 {
		return errors.New("ambiguity review requires suggested preference options")
	}
	seen := make(map[string]struct{}, len(proposal.CompetingTools))
	for _, member := range proposal.CompetingTools {
		if _, ok := known[member]; !ok {
			return fmt.Errorf("ambiguity review references unknown tool %q", member)
		}
		if _, duplicate := seen[member]; duplicate {
			return fmt.Errorf("ambiguity review repeats tool %q", member)
		}
		seen[member] = struct{}{}
		details, ok := proposal.ProsCons[member]
		if !ok || len(details.Pros) == 0 || len(details.Cons) == 0 {
			return fmt.Errorf("ambiguity review requires pros and cons for %q", member)
		}
	}
	return nil
}

func capabilityReconciliationGate(sources []catalog.SourceToolRecord) (string, string) {
	type sourceIdentity struct {
		MemberKey   string `json:"member_key"`
		Fingerprint string `json:"source_fingerprint"`
	}
	items := make([]sourceIdentity, 0, len(sources))
	for _, source := range sources {
		items = append(items, sourceIdentity{MemberKey(source.ServerID, source.ToolName), source.SourceFingerprint})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].MemberKey < items[j].MemberKey })
	body, _ := json.Marshal(struct {
		Protocol string           `json:"protocol"`
		Sources  []sourceIdentity `json:"sources"`
	}{CapabilityProtocolVersion, items})
	return "capability-reconciliation:global", toolcontract.FingerprintJSON(body)
}

func deterministicBatchID(generationID string, kind catalog.EnrichmentBatchKind, batchKey string, request []byte) string {
	body, _ := json.Marshal(struct {
		Generation string `json:"generation"`
		Kind       string `json:"kind"`
		BatchKey   string `json:"batch_key"`
		Request    string `json:"request_fingerprint"`
	}{generationID, string(kind), batchKey, toolcontract.FingerprintJSON(request)})
	return "batch:" + strings.TrimPrefix(toolcontract.FingerprintJSON(body), "sha256:")
}

func projectionFingerprint(version, text string) string {
	digest := sha256.Sum256([]byte(version + "\x00" + text))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func fingerprintText(text string) string {
	digest := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func shortFingerprint(body []byte) string {
	fingerprint := strings.TrimPrefix(toolcontract.FingerprintJSON(body), "sha256:")
	if len(fingerprint) > 12 {
		return fingerprint[:12]
	}
	return fingerprint
}
