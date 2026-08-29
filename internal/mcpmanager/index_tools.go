package mcpmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/indexing"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
	Kind  catalog.EnrichmentBatchKind `json:"kind" jsonschema:"Required batch kind: tool_enrichment, capability_reconciliation, or ambiguity_review."`
	Limit int                         `json:"limit,omitempty" jsonschema:"Maximum number of immutable pending work items to return. Defaults to 1."`
}

// indexEnrichmentBatch is the MCP-facing projection of a persisted enrichment
// batch. The catalog intentionally stores request/accepted-response bodies as
// json.RawMessage, but reflecting RawMessage directly into an MCP output schema
// describes it as []byte (an array). On the wire these fields are arbitrary JSON
// values, so decode them before handing the object to the typed MCP tool layer.
type indexEnrichmentBatch struct {
	ID                   string                      `json:"batch_id"`
	GenerationID         string                      `json:"generation_id"`
	Kind                 catalog.EnrichmentBatchKind `json:"kind"`
	BatchKey             string                      `json:"batch_key"`
	Required             bool                        `json:"required"`
	RequestFingerprint   string                      `json:"request_fingerprint"`
	Request              any                         `json:"request" jsonschema:"Immutable enrichment request JSON for the declared batch protocol."`
	AcceptedFingerprint  string                      `json:"accepted_fingerprint,omitempty"`
	AcceptedResponse     any                         `json:"accepted_response,omitempty" jsonschema:"Previously accepted agent response JSON, when this batch has already been accepted."`
	CreatedAt            time.Time                   `json:"created_at"`
	AcceptedAt           *time.Time                  `json:"accepted_at,omitempty"`
}

type indexGetBatchOutput struct {
	Batches []indexEnrichmentBatch `json:"batches,omitempty"`
	Error   *toolError              `json:"error,omitempty"`
}

type indexSubmitBatchInput struct {
	BatchID  string `json:"batch_id" jsonschema:"Immutable enrichment batch identifier returned by index_get_enrichment_batch."`
	Response any    `json:"response" jsonschema:"Agent-produced response matching the protocol and request carried by the immutable batch."`
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
		Description: "Report active/staging generation readiness, pending required enrichment work, and open non-blocking Ambiguity Reviews separately.",
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
		Description: "Return immutable unclaimed enrichment work. Required tool_enrichment and capability_reconciliation work blocks commit; ambiguity_review work is optional and non-blocking.",
		Annotations: &mcp.ToolAnnotations{Title: "Get enrichment work", ReadOnlyHint: true, DestructiveHint: &nondestructive, IdempotentHint: true, OpenWorldHint: &closed},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input indexGetBatchInput) (*mcp.CallToolResult, indexGetBatchOutput, error) {
		if service == nil {
			return indexBatchFailure(errors.New("manager_index_unavailable"))
		}
		batches, err := service.GetBatch(ctx, input.Kind, input.Limit)
		if err != nil {
			return indexBatchFailure(err)
		}
		projected, err := projectIndexBatches(batches)
		if err != nil {
			return indexBatchFailure(err)
		}
		return nil, indexGetBatchOutput{Batches: projected}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "index_submit_enrichment_batch", Title: "Submit enrichment work",
		Description: "Submit one immutable enrichment batch. The first valid response is accepted, an identical repeat is idempotent, and conflicting repeats fail closed.",
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

func projectIndexBatches(batches []catalog.EnrichmentBatch) ([]indexEnrichmentBatch, error) {
	projected := make([]indexEnrichmentBatch, 0, len(batches))
	for _, batch := range batches {
		item, err := projectIndexBatch(batch)
		if err != nil {
			return nil, err
		}
		projected = append(projected, item)
	}
	return projected, nil
}

func projectIndexBatch(batch catalog.EnrichmentBatch) (indexEnrichmentBatch, error) {
	request, err := decodeIndexJSONValue(batch.RequestJSON, "request", batch.ID)
	if err != nil {
		return indexEnrichmentBatch{}, err
	}
	var accepted any
	if len(batch.AcceptedResponseJSON) != 0 {
		accepted, err = decodeIndexJSONValue(batch.AcceptedResponseJSON, "accepted response", batch.ID)
		if err != nil {
			return indexEnrichmentBatch{}, err
		}
	}
	return indexEnrichmentBatch{
		ID: batch.ID,
		GenerationID: batch.GenerationID,
		Kind: batch.Kind,
		BatchKey: batch.BatchKey,
		Required: batch.Required,
		RequestFingerprint: batch.RequestFingerprint,
		Request: request,
		AcceptedFingerprint: batch.AcceptedFingerprint,
		AcceptedResponse: accepted,
		CreatedAt: batch.CreatedAt,
		AcceptedAt: batch.AcceptedAt,
	}, nil
}

func decodeIndexJSONValue(body json.RawMessage, field, batchID string) (any, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("enrichment batch %s has empty %s JSON", batchID, field)
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, fmt.Errorf("decode enrichment batch %s %s JSON: %w", batchID, field, err)
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
