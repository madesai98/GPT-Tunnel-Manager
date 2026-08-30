package enrichment

import (
	"encoding/json"
	"fmt"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
)

// BatchProtocolDescriptor is the self-describing contract an agent needs to
// complete an enrichment batch without any GPT Tunnel Manager-specific prompt,
// skill, or prior protocol knowledge.
type BatchProtocolDescriptor struct {
	Protocol          string
	ResponseSchema    any
	AgentInstructions []string
}

const toolEnrichmentResponseSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["items"],
  "properties": {
    "items": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["member_key", "guidance"],
        "properties": {
          "member_key": {"type": "string", "minLength": 1},
          "guidance": {
            "type": "object",
            "additionalProperties": false,
            "required": ["purpose"],
            "properties": {
              "purpose": {"type": "string", "minLength": 1},
              "use_when": {"type": "array", "items": {"type": "string"}},
              "avoid_when": {"type": "array", "items": {"type": "string"}},
              "examples": {"type": "array", "items": {"type": "string"}},
              "argument_guidance": {"type": "object", "additionalProperties": {"type": "string"}},
              "preconditions": {"type": "array", "items": {"type": "string"}},
              "output_interpretation": {"type": "string"},
              "alternatives": {"type": "array", "items": {"type": "string"}},
              "capabilities": {"type": "array", "items": {"type": "string"}}
            }
          }
        }
      }
    }
  }
}`

const capabilityReconciliationResponseSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["hierarchy"],
  "properties": {
    "hierarchy": {
      "type": "object",
      "additionalProperties": false,
      "required": ["protocol", "capabilities"],
      "properties": {
        "protocol": {"const": "capability-reconciliation/v1"},
        "capabilities": {
          "type": "array",
          "items": {
            "type": "object",
            "additionalProperties": false,
            "required": ["id", "name"],
            "properties": {
              "id": {"type": "string", "minLength": 1},
              "name": {"type": "string", "minLength": 1},
              "description": {"type": "string"},
              "parent_id": {"type": "string"},
              "tool_members": {"type": "array", "items": {"type": "string", "minLength": 1}}
            }
          }
        }
      }
    },
    "ambiguities": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["summary", "competing_tools", "pros_cons", "conditional_use_cases", "suggested_options"],
        "properties": {
          "summary": {"type": "string", "minLength": 1},
          "competing_tools": {"type": "array", "minItems": 2, "items": {"type": "string", "minLength": 1}},
          "pros_cons": {
            "type": "object",
            "additionalProperties": {
              "type": "object",
              "additionalProperties": false,
              "properties": {
                "pros": {"type": "array", "items": {"type": "string"}},
                "cons": {"type": "array", "items": {"type": "string"}}
              }
            }
          },
          "conditional_use_cases": {"type": "array", "minItems": 1, "items": {"type": "string"}},
          "suggested_options": {"type": "array", "minItems": 1, "items": {"type": "string"}}
        }
      }
    }
  }
}`

const ambiguityReviewResponseSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "oneOf": [
    {
      "type": "object",
      "additionalProperties": false,
      "required": ["resolution"],
      "properties": {
        "resolution": {"const": "neutral"},
        "preference_ids": {"type": "array", "maxItems": 0, "items": {"type": "string"}}
      }
    },
    {
      "type": "object",
      "additionalProperties": false,
      "required": ["resolution", "preference_ids"],
      "properties": {
        "resolution": {"const": "preference"},
        "preference_ids": {"type": "array", "minItems": 1, "items": {"type": "string", "minLength": 1}}
      }
    }
  ]
}`

// ProtocolDescriptorForBatchKind returns the exact response contract plus
// protocol-local instructions that are dynamically injected into Manager MCP
// batch results. Keep this colocated with the Go response types so protocol
// changes cannot depend on external prompt documentation.
func ProtocolDescriptorForBatchKind(kind catalog.EnrichmentBatchKind) (BatchProtocolDescriptor, error) {
	var protocol, schemaText string
	var instructions []string
	switch kind {
	case catalog.BatchToolEnrichment:
		protocol = ToolEnrichmentProtocolVersion
		schemaText = toolEnrichmentResponseSchema
		instructions = []string{
			"Return exactly one items[] result for every request item, using the exact tool.member_key.",
			"Read the complete authoritative tool contract and semantic neighbors before writing guidance. Describe what the tool actually does and the intents that should retrieve it; do not merely restate its name.",
			"guidance.purpose is required. Add use_when, avoid_when, argument_guidance, preconditions, output_interpretation, capabilities, alternatives, and examples when they materially improve routing.",
			"Use exact member keys when naming alternatives. Preserve meaningful distinctions between similar tools instead of collapsing them by server or superficial naming.",
			"After submitting this batch successfully, continue fetching and submitting tool_enrichment batches until none remain; then fetch capability_reconciliation. Do not stop merely because additional enrichment work is pending.",
		}
	case catalog.BatchCapabilityReconciliation:
		protocol = CapabilityProtocolVersion
		schemaText = capabilityReconciliationResponseSchema
		instructions = []string{
			"Build a semantic capability hierarchy from the supplied authoritative tool contracts and completed tool enrichment.",
			"Set hierarchy.protocol to capability-reconciliation/v1 and use tool_members exactly; do not invent aliases such as tools.",
			"Every supplied tool.member_key must be assigned to at least one capability. Capability ids and names must be non-empty, parent_id must reference another returned capability, and the hierarchy must be acyclic.",
			"Group by actual user-facing capability, not merely by source server. Preserve distinctions when tools differ by interaction mode, runtime, build/compile/test purpose, application domain, live vs headless execution, UI state vs screenshots vs rendering, or other routing-relevant behavior visible in the contracts.",
			"Only emit ambiguities when two or more tools genuinely compete for the same intent. For each ambiguity include source-grounded pros/cons, conditional use cases, and actionable suggested options.",
			"After successful submission, process available ambiguity_review batches when present, then commit once index_status reports pending_required=0. Open Ambiguity Reviews are non-blocking, but process those that can be resolved without inventing user preferences.",
		}
	case catalog.BatchAmbiguityReview:
		protocol = AmbiguityReviewProtocolVersion
		schemaText = ambiguityReviewResponseSchema
		instructions = []string{
			"Review the supplied ambiguity proposal using its source-grounded distinctions.",
			"Return resolution=neutral when no persisted routing preference is warranted.",
			"Return resolution=preference only when the relevant routing preference has already been persisted, and include at least one exact persisted preference id in preference_ids.",
			"Ambiguity Reviews are non-blocking; do not invent a preference merely to close the review.",
			"Continue through all available ambiguity_review batches. When required enrichment is complete, commit the staging generation and verify index_status reports ready=true with the committed generation active.",
		}
	default:
		return BatchProtocolDescriptor{}, fmt.Errorf("unsupported enrichment batch kind %q", kind)
	}
	instructions = append(instructions,
		"Submit only JSON matching response_schema. If submission is rejected for schema or validation reasons, use the returned error immediately to correct and retry; do not search for private protocol documentation or ask the user unless there is an actual runtime or configuration blocker.",
	)
	var schema any
	if err := json.Unmarshal([]byte(schemaText), &schema); err != nil {
		return BatchProtocolDescriptor{}, fmt.Errorf("decode %s response schema: %w", protocol, err)
	}
	return BatchProtocolDescriptor{Protocol: protocol, ResponseSchema: schema, AgentInstructions: instructions}, nil
}
