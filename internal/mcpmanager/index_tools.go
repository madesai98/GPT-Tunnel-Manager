package mcpmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/indexing"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultIndexRequestPageItems = 16
	maxIndexRequestPageItems     = 64
	maxIndexRequestPageBytes     = 128 * 1024
)

var indexGetEnrichmentBatchInputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "kind": {
      "type": "string",
      "enum": ["tool_enrichment", "capability_reconciliation", "ambiguity_review"],
      "description": "Required batch kind."
    },
    "limit": {
      "type": "integer",
      "minimum": 0,
      "description": "Maximum pending batches to return. Defaults to 1. Compatibility: for the single global capability_reconciliation batch, when request_offset is unavailable in a stale client schema, set limit to request_page.next_offset to fetch that request page."
    },
    "request_offset": {
      "type": "integer",
      "minimum": 0,
      "description": "Zero-based item offset within each returned batch's immutable request. Use request_page.next_offset to fetch the next page."
    },
    "request_item_limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 64,
      "description": "Maximum request items per returned batch page. Defaults to 16; pages may contain fewer items to keep the MCP result bounded."
    }
  },
  "required": ["kind"],
  "additionalProperties": false
}`)

type indexStatusOutput struct {
	Status *indexing.Status `json:"status,omitempty"`
	Error  *toolError       `json:"error,omitempty"`
}

type indexRefreshInput struct {
	Force bool `json:"force,omitempty" jsonschema:"Force a new explicit refresh pass over the current staging generation. This never runs implicitly from ordinary configuration writes."`
}

type indexRefreshOutput struct {
	Result *indexing.RefreshResult `json:"result,omitempty"`
	Error  *toolError              `json:"error,omitempty"`
}

type indexGetBatchInput struct {
	Kind             catalog.EnrichmentBatchKind `json:"kind" jsonschema:"Required batch kind: tool_enrichment, capability_reconciliation, or ambiguity_review."`
	Limit            int                         `json:"limit,omitempty" jsonschema:"Maximum number of immutable pending batches to return. Defaults to 1. For stale clients that do not expose request_offset, capability_reconciliation may use limit=request_page.next_offset as a compatibility cursor."`
	RequestOffset    int                         `json:"request_offset,omitempty" jsonschema:"Zero-based item offset within each returned batch's immutable request. Use request_page.next_offset to fetch the next page of a large request."`
	RequestItemLimit int                         `json:"request_item_limit,omitempty" jsonschema:"Maximum request items per returned batch page. Defaults to 16 and is capped at 64; pages may contain fewer items to keep the MCP result bounded."`
}

type indexRequestPage struct {
	Offset     int  `json:"offset"`
	Returned   int  `json:"returned"`
	TotalItems int  `json:"total_items"`
	NextOffset int  `json:"next_offset,omitempty"`
	Complete   bool `json:"complete"`
}

// indexEnrichmentBatch is the MCP-facing projection of a persisted enrichment
// batch. The catalog intentionally stores request/accepted-response bodies as
// json.RawMessage, but reflecting RawMessage directly into an MCP output schema
// describes it as []byte (an array). On the wire these fields are JSON objects,
// so decode them before handing the object to the typed MCP tool layer.
//
// Protocol metadata is injected here at read time, so even batches persisted by
// an older Manager become fully self-describing after an upgrade. Large request
// item arrays are projected as deterministic pages so a conversational MCP
// client never has to consume a multi-megabyte reconciliation result in one call.
type indexEnrichmentBatch struct {
	ID                  string                      `json:"batch_id"`
	GenerationID        string                      `json:"generation_id"`
	Kind                catalog.EnrichmentBatchKind `json:"kind"`
	BatchKey            string                      `json:"batch_key"`
	Required            bool                        `json:"required"`
	RequestFingerprint  string                      `json:"request_fingerprint"`
	Protocol            string                      `json:"protocol" jsonschema:"Enrichment protocol version that governs this batch and its response."`
	ResponseSchema      map[string]any              `json:"response_schema" jsonschema:"Exact JSON Schema object for the response value accepted by index_submit_enrichment_batch. Follow this schema instead of guessing protocol fields."`
	ResponseSchemaJSON  string                      `json:"response_schema_json" jsonschema:"Exact response JSON Schema serialized as JSON text. This duplicates response_schema deliberately so clients that handle free-form nested schemas poorly still receive the complete contract."`
	AgentInstructions   []string                    `json:"agent_instructions" jsonschema:"Protocol-local instructions injected by GPT Tunnel Manager so a fresh agent can complete the batch without external prompt or skill context."`
	Request             map[string]any              `json:"request" jsonschema:"Deterministic page of the immutable enrichment request JSON. When request_page.complete is false, fetch every subsequent page before constructing the batch response."`
	RequestPage         *indexRequestPage           `json:"request_page,omitempty" jsonschema:"Pagination metadata for request.items. Pages with the same batch_id and request_fingerprint together reconstruct the exact immutable request."`
	AcceptedFingerprint string                      `json:"accepted_fingerprint,omitempty"`
	AcceptedResponse    map[string]any              `json:"accepted_response,omitempty" jsonschema:"Previously accepted agent response JSON, when this batch has already been accepted."`
	CreatedAt           time.Time                   `json:"created_at"`
	AcceptedAt          *time.Time                  `json:"accepted_at,omitempty"`
}

type indexGetBatchOutput struct {
	Batches []indexEnrichmentBatch `json:"batches,omitempty"`
	Error   *toolError              `json:"error,omitempty"`
}

type indexSubmitBatchInput struct {
	BatchID  string `json:"batch_id" jsonschema:"Immutable enrichment batch identifier returned by index_get_enrichment_batch."`
	Response any    `json:"response" jsonschema:"Agent-produced response. Fetch every request page first, then follow response_schema and agent_instructions exactly. Unknown response fields are rejected."`
}

type indexSubmitBatchOutput struct {
	Batch      *indexEnrichmentBatch `json:"batch,omitempty"`
	Idempotent bool                  `json:"idempotent,omitempty"`
	Error      *toolError            `json:"error,omitempty"`
}

type indexCommitOutput struct {
	Result *indexing.CommitResult `json:"result,omitempty"`
	Error  *toolError             `json:"error,omitempty"`
}

func registerV2IndexTools(server *mcp.Server, service *indexing.Service) {
	closed := false
	open := true
	nondestructive := false

	mcp.AddTool(server, &mcp.Tool{
		Name: "index_status", Title: "Get index status",
		Description: "Report active/staging generation readiness, per-kind enrichment state, explicit promotion blockers, the next autonomous action, and open non-blocking Ambiguity Reviews.",
		Annotations: &mcp.ToolAnnotations{Title: "Get index status", ReadOnlyHint: true, DestructiveHint: &nondestructive, IdempotentHint: true, OpenWorldHint: &closed},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, indexStatusOutput, error) {
		if service == nil {
			return indexStatusFailure(errors.New("manager_index_unavailable"))
		}
		status, err := service.Status(ctx)
		if err != nil {
			return indexStatusFailure(err)
		}
		return nil, indexStatusOutput{Status: &status}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "index_refresh", Title: "Prepare or refresh index",
		Description: "Explicitly start or resume a staging generation, discover enabled downstream source contracts through lifecycle-aware leases, build/reuse base retrieval artifacts, and prepare required tool-enrichment work.",
		Annotations: &mcp.ToolAnnotations{Title: "Prepare or refresh index", ReadOnlyHint: false, DestructiveHint: &nondestructive, IdempotentHint: true, OpenWorldHint: &open},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input indexRefreshInput) (*mcp.CallToolResult, indexRefreshOutput, error) {
		_ = input.Force // Refresh is already explicit and resumable; repeated calls are idempotent.
		if service == nil {
			return indexRefreshFailure(errors.New("manager_index_unavailable"))
		}
		result, err := service.Refresh(ctx)
		if err != nil {
			return indexRefreshFailure(err)
		}
		return nil, indexRefreshOutput{Result: &result}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "index_get_enrichment_batch", Title: "Get enrichment work",
		Description: "Return immutable unclaimed enrichment work with the exact response schema and protocol-local agent instructions. Large request.items arrays are returned in deterministic bounded pages; follow request_page.next_offset until complete before submitting. Fresh clients should use request_offset. A stale client whose cached schema exposes only kind and limit can fetch capability-reconciliation pages with limit=request_page.next_offset. Required tool_enrichment and capability_reconciliation work blocks commit; ambiguity_review work is optional and non-blocking.",
		InputSchema: indexGetEnrichmentBatchInputSchema,
		Annotations: &mcp.ToolAnnotations{Title: "Get enrichment work", ReadOnlyHint: true, DestructiveHint: &nondestructive, IdempotentHint: true, OpenWorldHint: &closed},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input indexGetBatchInput) (*mcp.CallToolResult, indexGetBatchOutput, error) {
		if service == nil {
			return indexBatchFailure(errors.New("manager_index_unavailable"))
		}
		batchLimit, requestOffset := resolveIndexGetBatchPaging(input)
		batches, err := service.GetBatch(ctx, input.Kind, batchLimit)
		if err != nil {
			return indexBatchFailure(err)
		}
		projected, err := projectIndexBatchesPage(batches, requestOffset, input.RequestItemLimit)
		if err != nil {
			return indexBatchFailure(err)
		}
		return nil, indexGetBatchOutput{Batches: projected}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "index_submit_enrichment_batch", Title: "Submit enrichment work",
		Description: "Submit one immutable enrichment batch after reading every request page and following the response_schema returned with that batch. Unknown fields are rejected. The first valid response is accepted, an identical repeat is idempotent, and conflicting repeats fail closed.",
		Annotations: &mcp.ToolAnnotations{Title: "Submit enrichment work", ReadOnlyHint: false, DestructiveHint: &nondestructive, IdempotentHint: true, OpenWorldHint: &closed},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input indexSubmitBatchInput) (*mcp.CallToolResult, indexSubmitBatchOutput, error) {
		if service == nil {
			return indexSubmitFailure(errors.New("manager_index_unavailable"))
		}
		result, err := service.SubmitBatch(ctx, input.BatchID, input.Response)
		if err != nil {
			return indexSubmitFailure(err)
		}
		batch, err := projectIndexBatch(result.Batch)
		if err != nil {
			return indexSubmitFailure(err)
		}
		return nil, indexSubmitBatchOutput{Batch: &batch, Idempotent: result.Idempotent}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "index_commit", Title: "Commit prepared index",
		Description: "Atomically promote the current staging generation after all required work is complete. Open Ambiguity Reviews do not block commit.",
		Annotations: &mcp.ToolAnnotations{Title: "Commit prepared index", ReadOnlyHint: false, DestructiveHint: &nondestructive, IdempotentHint: false, OpenWorldHint: &closed},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, indexCommitOutput, error) {
		if service == nil {
			return indexCommitFailure(errors.New("manager_index_unavailable"))
		}
		result, err := service.Commit(ctx)
		if err != nil {
			return indexCommitFailure(err)
		}
		return nil, indexCommitOutput{Result: &result}, nil
	})
}

func resolveIndexGetBatchPaging(input indexGetBatchInput) (batchLimit, requestOffset int) {
	batchLimit = input.Limit
	requestOffset = input.RequestOffset
	if input.Kind == catalog.BatchCapabilityReconciliation {
		// Capability reconciliation is canonically one global batch. That makes
		// positive limit values above 1 an otherwise-unused compatibility channel
		// for clients that cached the pre-pagination two-field MCP input schema.
		batchLimit = 1
		if requestOffset == 0 && input.Limit > 1 {
			requestOffset = input.Limit
		}
	}
	return batchLimit, requestOffset
}

func projectIndexBatches(batches []catalog.EnrichmentBatch) ([]indexEnrichmentBatch, error) {
	return projectIndexBatchesPage(batches, 0, defaultIndexRequestPageItems)
}

func projectIndexBatchesPage(batches []catalog.EnrichmentBatch, offset, itemLimit int) ([]indexEnrichmentBatch, error) {
	projected := make([]indexEnrichmentBatch, 0, len(batches))
	for _, batch := range batches {
		item, err := projectIndexBatchPage(batch, offset, itemLimit)
		if err != nil {
			return nil, err
		}
		projected = append(projected, item)
	}
	return projected, nil
}

func projectIndexBatch(batch catalog.EnrichmentBatch) (indexEnrichmentBatch, error) {
	return projectIndexBatchPage(batch, 0, defaultIndexRequestPageItems)
}

func projectIndexBatchPage(batch catalog.EnrichmentBatch, offset, itemLimit int) (indexEnrichmentBatch, error) {
	request, err := decodeIndexJSONObject(batch.RequestJSON, "request", batch.ID)
	if err != nil {
		return indexEnrichmentBatch{}, err
	}
	descriptor, err := enrichment.ProtocolDescriptorForBatchKind(batch.Kind)
	if err != nil {
		return indexEnrichmentBatch{}, err
	}
	if err := validateProjectedBatchProtocol(request, descriptor.Protocol, batch.ID); err != nil {
		return indexEnrichmentBatch{}, err
	}
	pagedRequest, requestPage, err := pageIndexRequest(request, offset, itemLimit, batch.ID)
	if err != nil {
		return indexEnrichmentBatch{}, err
	}
	responseSchema, ok := descriptor.ResponseSchema.(map[string]any)
	if !ok {
		return indexEnrichmentBatch{}, fmt.Errorf("enrichment batch %s response schema is not a JSON object", batch.ID)
	}
	responseSchemaBody, err := json.Marshal(responseSchema)
	if err != nil {
		return indexEnrichmentBatch{}, fmt.Errorf("encode enrichment batch %s response schema: %w", batch.ID, err)
	}
	var accepted map[string]any
	if len(batch.AcceptedResponseJSON) != 0 {
		accepted, err = decodeIndexJSONObject(batch.AcceptedResponseJSON, "accepted response", batch.ID)
		if err != nil {
			return indexEnrichmentBatch{}, err
		}
	}
	instructions := append([]string(nil), descriptor.AgentInstructions...)
	if batch.Kind == catalog.BatchCapabilityReconciliation {
		instructions = append(instructions, "If your MCP client rejects request_offset because it cached an older index_get_enrichment_batch schema that exposes only kind and limit, continue without restarting enrichment: call index_get_enrichment_batch with kind=capability_reconciliation and limit=request_page.next_offset. Because capability reconciliation is one global batch, Manager treats limit>1 as that page offset when request_offset is omitted. Keep collecting pages until request_page.complete=true.")
	}
	return indexEnrichmentBatch{
		ID:                  batch.ID,
		GenerationID:        batch.GenerationID,
		Kind:                batch.Kind,
		BatchKey:            batch.BatchKey,
		Required:            batch.Required,
		RequestFingerprint:  batch.RequestFingerprint,
		Protocol:            descriptor.Protocol,
		ResponseSchema:      responseSchema,
		ResponseSchemaJSON:  string(responseSchemaBody),
		AgentInstructions:   instructions,
		Request:             pagedRequest,
		RequestPage:         requestPage,
		AcceptedFingerprint: batch.AcceptedFingerprint,
		AcceptedResponse:    accepted,
		CreatedAt:           batch.CreatedAt,
		AcceptedAt:          batch.AcceptedAt,
	}, nil
}

func pageIndexRequest(request map[string]any, offset, itemLimit int, batchID string) (map[string]any, *indexRequestPage, error) {
	if offset < 0 {
		return nil, nil, fmt.Errorf("enrichment batch %s request_offset must be non-negative", batchID)
	}
	itemsValue, hasItems := request["items"]
	if !hasItems {
		if offset != 0 {
			return nil, nil, fmt.Errorf("enrichment batch %s request has no pageable items; request_offset must be zero", batchID)
		}
		return cloneJSONObject(request), nil, nil
	}
	items, ok := itemsValue.([]any)
	if !ok {
		return nil, nil, fmt.Errorf("enrichment batch %s request.items is not a JSON array", batchID)
	}
	if offset > len(items) {
		return nil, nil, fmt.Errorf("enrichment batch %s request_offset %d exceeds total item count %d", batchID, offset, len(items))
	}
	if itemLimit <= 0 {
		itemLimit = defaultIndexRequestPageItems
	}
	if itemLimit > maxIndexRequestPageItems {
		itemLimit = maxIndexRequestPageItems
	}
	end := offset + itemLimit
	if end > len(items) {
		end = len(items)
	}
	// Item count is only an upper bound. Shrink the page until the serialized
	// request fits a conservative conversational-MCP payload budget. A single
	// oversized item is still returned so the batch can never become unpageable.
	for end > offset+1 {
		candidate := cloneJSONObject(request)
		candidate["items"] = append([]any(nil), items[offset:end]...)
		body, err := json.Marshal(candidate)
		if err != nil {
			return nil, nil, fmt.Errorf("encode enrichment batch %s request page: %w", batchID, err)
		}
		if len(body) <= maxIndexRequestPageBytes {
			break
		}
		end--
	}
	paged := cloneJSONObject(request)
	paged["items"] = append([]any(nil), items[offset:end]...)
	page := &indexRequestPage{
		Offset:     offset,
		Returned:   end - offset,
		TotalItems: len(items),
		Complete:   end == len(items),
	}
	if !page.Complete {
		page.NextOffset = end
	}
	return paged, page, nil
}

func cloneJSONObject(value map[string]any) map[string]any {
	cloned := make(map[string]any, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

func validateProjectedBatchProtocol(request map[string]any, expected, batchID string) error {
	protocol, ok := request["protocol"].(string)
	if !ok || protocol == "" {
		return fmt.Errorf("enrichment batch %s request is missing protocol", batchID)
	}
	if protocol != expected {
		return fmt.Errorf("enrichment batch %s protocol %q does not match kind contract %q", batchID, protocol, expected)
	}
	return nil
}

func decodeIndexJSONObject(body json.RawMessage, field, batchID string) (map[string]any, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("enrichment batch %s has empty %s JSON", batchID, field)
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, fmt.Errorf("decode enrichment batch %s %s JSON: %w", batchID, field, err)
	}
	if value == nil {
		return nil, fmt.Errorf("enrichment batch %s %s JSON is not an object", batchID, field)
	}
	return value, nil
}

func indexStatusFailure(err error) (*mcp.CallToolResult, indexStatusOutput, error) {
	return &mcp.CallToolResult{IsError: true}, indexStatusOutput{Error: stableIndexError(err)}, nil
}
func indexRefreshFailure(err error) (*mcp.CallToolResult, indexRefreshOutput, error) {
	return &mcp.CallToolResult{IsError: true}, indexRefreshOutput{Error: stableIndexError(err)}, nil
}
func indexBatchFailure(err error) (*mcp.CallToolResult, indexGetBatchOutput, error) {
	return &mcp.CallToolResult{IsError: true}, indexGetBatchOutput{Error: stableIndexError(err)}, nil
}
func indexSubmitFailure(err error) (*mcp.CallToolResult, indexSubmitBatchOutput, error) {
	return &mcp.CallToolResult{IsError: true}, indexSubmitBatchOutput{Error: stableIndexError(err)}, nil
}
func indexCommitFailure(err error) (*mcp.CallToolResult, indexCommitOutput, error) {
	return &mcp.CallToolResult{IsError: true}, indexCommitOutput{Error: stableIndexError(err)}, nil
}

func stableIndexError(err error) *toolError {
	result := &toolError{Code: "operation_failed", Message: err.Error(), Retryable: true}
	var indexErr *indexing.Error
	if errors.As(err, &indexErr) {
		result.Code = indexErr.Code
		result.Retryable = indexErr.Code == indexing.CodeManualServerStoppedForIndex
		return result
	}
	switch {
	case errors.Is(err, catalog.ErrEnrichmentBatchConflict):
		result.Code = "enrichment_batch_conflict"
		result.Retryable = false
	case errors.Is(err, catalog.ErrGenerationNotStaging):
		result.Code = "generation_not_staging"
		result.Retryable = false
	case err.Error() == "manager_index_unavailable":
		result.Code = "manager_unavailable"
	}
	return result
}
