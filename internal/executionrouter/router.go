package executionrouter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/executionhandle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingstate"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const DefaultMaxResultBytes = 8 << 20

const (
	CodeManualServerStopped = "manual_server_stopped"
	CodeServerDisabled      = "server_disabled"
	CodeServerBusy          = "server_busy"
	CodeManagerShuttingDown = "manager_shutting_down"
)

type Outcome string

const (
	OutcomeNotStarted    Outcome = "not_started"
	OutcomeCompleted     Outcome = "completed"
	OutcomeUnknown       Outcome = "outcome_unknown"
	CodeIndexRequired            = "index_required"
	CodeInvalidHandle            = "invalid_execution_handle"
	CodeStaleHandle              = "stale_execution_handle"
	CodeExecutorMismatch         = "executor_class_mismatch"
	CodeInvalidArguments         = "invalid_arguments"
	CodeUnsupportedSchema        = "unsupported_tool_schema"
	CodeDownstreamUnavailable    = "downstream_unavailable"
	CodeDownstreamCallFailed     = "downstream_call_failed"
	CodeResultTooLarge           = "result_too_large"
	CodeDownstreamResultInvalid  = "downstream_result_invalid"
	CodeManagerUnavailable       = "manager_unavailable"
	CodeInvalidRequest           = "invalid_request"
)

type Input struct {
	ExecutionHandle string         `json:"execution_handle" jsonschema:"Authenticated execution handle returned by get_tool. This is the sole routing authority."`
	ToolName        string         `json:"tool_name,omitempty" jsonschema:"Optional human-readable downstream tool name for confirmation or logging only. It is not routing authority."`
	Arguments       map[string]any `json:"arguments,omitempty" jsonschema:"Arguments for the authoritative downstream tool."`
}

type ExecutionError struct {
	Code              string              `json:"code"`
	Message           string              `json:"message"`
	Outcome           Outcome             `json:"outcome"`
	Retryable         bool                `json:"retryable"`
	DownstreamIsError *bool               `json:"downstream_is_error,omitempty"`
	OriginalResult    *mcp.CallToolResult `json:"-"`
	cause             error
}

func (e *ExecutionError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *ExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

type RoutingState interface {
	Snapshot(context.Context) (routingstate.Snapshot, error)
	AdvanceRoutingRevision(context.Context) (routingstate.Snapshot, error)
}

type Session interface {
	InitialTools() downstream.ToolSnapshot
	CallTool(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error)
}

type SessionProvider interface {
	Session(context.Context, string) (Session, error)
}

type SessionProviderFunc func(context.Context, string) (Session, error)

func (f SessionProviderFunc) Session(ctx context.Context, serverID string) (Session, error) {
	return f(ctx, serverID)
}

// sessionLease is intentionally optional so Phase 8 test/fixture providers and
// any non-lifecycle session providers remain source-compatible. Phase 9's
// router-native provider returns a leased session; Execute defers Release across
// every path after acquisition, including contract checks and result handling.
type sessionLease interface {
	Release()
}

// providerExecutionError lets the lifecycle provider surface stable blockers
// (Manual stopped, Disabled, shutdown, etc.) without coupling the execution
// router to a concrete lifecycle package.
type providerExecutionError interface {
	error
	ExecutionCode() string
	ExecutionRetryable() bool
}

type Options struct {
	MaxResultBytes int
}

type Service struct {
	catalog        *catalog.Catalog
	state          RoutingState
	handles        *executionhandle.Manager
	sessions       SessionProvider
	maxResultBytes int
}

func NewService(c *catalog.Catalog, state RoutingState, handles *executionhandle.Manager, sessions SessionProvider, opts Options) (*Service, error) {
	if c == nil {
		return nil, errors.New("catalog is required")
	}
	if state == nil {
		return nil, errors.New("routing state is required")
	}
	if handles == nil {
		return nil, errors.New("execution handle manager is required")
	}
	if sessions == nil {
		return nil, errors.New("downstream session provider is required")
	}
	limit := opts.MaxResultBytes
	if limit <= 0 {
		limit = DefaultMaxResultBytes
	}
	return &Service{catalog: c, state: state, handles: handles, sessions: sessions, maxResultBytes: limit}, nil
}

func (s *Service) Execute(ctx context.Context, executor toolcontract.ExecutorClass, input Input) (*mcp.CallToolResult, *ExecutionError) {
	if !knownExecutorClass(executor) {
		return nil, executionFailure(CodeInvalidRequest, "unknown Manager executor class", OutcomeNotStarted, false, nil)
	}

	claims, err := s.handles.Validate(strings.TrimSpace(input.ExecutionHandle))
	if err != nil {
		code := CodeInvalidHandle
		if errors.Is(err, executionhandle.ErrStaleHandle) {
			code = CodeStaleHandle
		}
		return nil, executionFailure(code, err.Error(), OutcomeNotStarted, false, err)
	}

	state, err := s.state.Snapshot(ctx)
	if err != nil {
		return nil, executionFailure(CodeManagerUnavailable, fmt.Sprintf("read routing state: %v", err), OutcomeNotStarted, true, err)
	}
	if strings.TrimSpace(state.RoutingStateHash) == "" {
		return nil, executionFailure(CodeIndexRequired, CodeIndexRequired, OutcomeNotStarted, false, nil)
	}
	generation, current, err := s.catalog.ActiveCurrent(ctx, state.RoutingStateHash)
	if err != nil {
		return nil, executionFailure(CodeManagerUnavailable, fmt.Sprintf("validate active generation: %v", err), OutcomeNotStarted, true, err)
	}
	if !current {
		return nil, executionFailure(CodeIndexRequired, CodeIndexRequired, OutcomeNotStarted, false, nil)
	}
	if claims.GenerationID != generation.ID {
		return nil, executionFailure(CodeStaleHandle, "execution handle generation is no longer active", OutcomeNotStarted, false, executionhandle.ErrStaleHandle)
	}

	if _, err := s.catalog.SourceServer(ctx, generation.ID, claims.ServerID); err != nil {
		return nil, executionFailure(CodeIndexRequired, "authoritative source server is unavailable in the active generation", OutcomeNotStarted, false, err)
	}
	sources, err := s.catalog.SourceTools(ctx, generation.ID)
	if err != nil {
		return nil, executionFailure(CodeManagerUnavailable, fmt.Sprintf("load authoritative tools: %v", err), OutcomeNotStarted, true, err)
	}
	source, ok := findSource(sources, claims.ServerID, claims.ToolName)
	if !ok {
		return nil, executionFailure(CodeStaleHandle, "execution handle tool is no longer authoritative", OutcomeNotStarted, false, executionhandle.ErrStaleHandle)
	}
	if source.SourceFingerprint != claims.SourceFingerprint {
		return nil, executionFailure(CodeStaleHandle, "execution handle source fingerprint no longer matches", OutcomeNotStarted, false, executionhandle.ErrStaleHandle)
	}
	class, err := toolcontract.ExecutorClassForJSON(source.ContractJSON)
	if err != nil {
		return nil, executionFailure(CodeIndexRequired, fmt.Sprintf("authoritative tool annotations are invalid: %v", err), OutcomeNotStarted, false, err)
	}
	if string(class) != claims.ExecutorClass {
		return nil, executionFailure(CodeStaleHandle, "execution handle executor class no longer matches", OutcomeNotStarted, false, executionhandle.ErrStaleHandle)
	}
	if class != executor {
		return nil, executionFailure(CodeExecutorMismatch, fmt.Sprintf("execution handle requires %s, not %s", class, executor), OutcomeNotStarted, false, nil)
	}

	tool, err := decodeAuthoritativeTool(source)
	if err != nil {
		return nil, executionFailure(CodeIndexRequired, err.Error(), OutcomeNotStarted, false, err)
	}
	if err := validateArguments(tool, input.Arguments); err != nil {
		if errors.Is(err, errUnsupportedSchema) {
			return nil, executionFailure(CodeUnsupportedSchema, err.Error(), OutcomeNotStarted, false, err)
		}
		return nil, executionFailure(CodeInvalidArguments, err.Error(), OutcomeNotStarted, false, err)
	}

	expectedSnapshot, err := authoritativeServerSnapshot(sources, claims.ServerID)
	if err != nil {
		return nil, executionFailure(CodeIndexRequired, err.Error(), OutcomeNotStarted, false, err)
	}
	session, err := s.sessions.Session(ctx, claims.ServerID)
	if err != nil {
		code := CodeDownstreamUnavailable
		retryable := true
		var classified providerExecutionError
		if errors.As(err, &classified) {
			if candidate := strings.TrimSpace(classified.ExecutionCode()); candidate != "" {
				code = candidate
			}
			retryable = classified.ExecutionRetryable()
		}
		return nil, executionFailure(code, fmt.Sprintf("downstream %s unavailable: %v", claims.ServerID, err), OutcomeNotStarted, retryable, err)
	}
	if session == nil {
		return nil, executionFailure(CodeManagerUnavailable, "downstream session provider returned a nil session", OutcomeNotStarted, true, nil)
	}
	if lease, ok := session.(sessionLease); ok {
		defer lease.Release()
	}

	liveSnapshot := session.InitialTools()
	if liveSnapshot.Fingerprint == "" || liveSnapshot.Fingerprint != expectedSnapshot.Fingerprint {
		if invalidateErr := s.invalidateContract(ctx, claims.ServerID, liveSnapshot.Fingerprint); invalidateErr != nil {
			return nil, executionFailure(CodeIndexRequired, fmt.Sprintf("%s: failed to persist live contract invalidation: %v", CodeIndexRequired, invalidateErr), OutcomeNotStarted, false, invalidateErr)
		}
		return nil, executionFailure(CodeIndexRequired, CodeIndexRequired, OutcomeNotStarted, false, downstream.ErrToolContractChanged)
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: claims.ToolName, Arguments: input.Arguments})
	if err != nil {
		switch {
		case errors.Is(err, downstream.ErrToolContractChanged):
			if invalidateErr := s.invalidateContract(ctx, claims.ServerID, ""); invalidateErr != nil {
				return nil, executionFailure(CodeIndexRequired, fmt.Sprintf("%s: failed to persist live contract invalidation: %v", CodeIndexRequired, invalidateErr), OutcomeNotStarted, false, invalidateErr)
			}
			return nil, executionFailure(CodeIndexRequired, CodeIndexRequired, OutcomeNotStarted, false, err)
		case errors.Is(err, downstream.ErrDownstreamUnavailable):
			return nil, executionFailure(CodeDownstreamUnavailable, err.Error(), OutcomeNotStarted, true, err)
		default:
			// Once the downstream CallTool boundary has been crossed, a transport
			// error cannot prove whether the operation ran. Initial v2 never
			// replays this call, including for idempotent executor classes.
			return nil, executionFailure(CodeDownstreamCallFailed, err.Error(), OutcomeUnknown, false, err)
		}
	}
	if result == nil {
		return nil, executionFailure(CodeDownstreamResultInvalid, "downstream returned a nil CallToolResult", OutcomeCompleted, false, nil)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		failure := executionFailure(CodeDownstreamResultInvalid, fmt.Sprintf("downstream result cannot be represented safely: %v", err), OutcomeCompleted, false, err)
		failure.OriginalResult = result
		isError := result.IsError
		failure.DownstreamIsError = &isError
		return nil, failure
	}
	if len(encoded) > s.maxResultBytes {
		isError := result.IsError
		failure := executionFailure(CodeResultTooLarge, fmt.Sprintf("downstream result is %d bytes; configured limit is %d bytes", len(encoded), s.maxResultBytes), OutcomeCompleted, false, nil)
		failure.DownstreamIsError = &isError
		return nil, failure
	}
	return result, nil
}

func (s *Service) invalidateContract(ctx context.Context, serverID, observedFingerprint string) error {
	if err := s.catalog.MarkDirty(ctx, "server:"+serverID, "live downstream tool contract changed", observedFingerprint); err != nil {
		return err
	}
	_, err := s.state.AdvanceRoutingRevision(ctx)
	return err
}

func findSource(sources []catalog.SourceToolRecord, serverID, toolName string) (catalog.SourceToolRecord, bool) {
	for _, source := range sources {
		if source.ServerID == serverID && source.ToolName == toolName {
			return source, true
		}
	}
	return catalog.SourceToolRecord{}, false
}

func decodeAuthoritativeTool(source catalog.SourceToolRecord) (*mcp.Tool, error) {
	var tool mcp.Tool
	if err := json.Unmarshal(source.ContractJSON, &tool); err != nil {
		return nil, fmt.Errorf("decode authoritative tool %s/%s: %w", source.ServerID, source.ToolName, err)
	}
	if tool.Name != source.ToolName {
		return nil, fmt.Errorf("authoritative invocation identity mismatch for %s/%s", source.ServerID, source.ToolName)
	}
	return &tool, nil
}

func authoritativeServerSnapshot(sources []catalog.SourceToolRecord, serverID string) (downstream.ToolSnapshot, error) {
	tools := make([]*mcp.Tool, 0)
	for _, source := range sources {
		if source.ServerID != serverID {
			continue
		}
		tool, err := decodeAuthoritativeTool(source)
		if err != nil {
			return downstream.ToolSnapshot{}, err
		}
		tools = append(tools, tool)
	}
	if len(tools) == 0 {
		return downstream.ToolSnapshot{}, fmt.Errorf("authoritative server %s has no tools", serverID)
	}
	fingerprint, canonical, err := toolcontract.FingerprintTools(tools)
	if err != nil {
		return downstream.ToolSnapshot{}, fmt.Errorf("fingerprint authoritative server %s tools: %w", serverID, err)
	}
	return downstream.ToolSnapshot{Tools: canonical, Fingerprint: fingerprint}, nil
}

var errUnsupportedSchema = errors.New("unsupported tool schema")

func validateArguments(tool *mcp.Tool, arguments map[string]any) error {
	if tool == nil || tool.InputSchema == nil {
		return fmt.Errorf("%w: input schema is missing", errUnsupportedSchema)
	}
	body, err := json.Marshal(tool.InputSchema)
	if err != nil {
		return fmt.Errorf("%w: encode input schema: %v", errUnsupportedSchema, err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(body, &schema); err != nil {
		return fmt.Errorf("%w: decode input schema: %v", errUnsupportedSchema, err)
	}
	// No Loader is supplied. jsonschema-go therefore resolves in-document '#'
	// references and $defs but rejects every external URI/path reference without
	// performing network or filesystem I/O.
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return fmt.Errorf("%w: resolve input schema locally: %v", errUnsupportedSchema, err)
	}
	if arguments == nil {
		arguments = map[string]any{}
	}
	if err := resolved.Validate(arguments); err != nil {
		return fmt.Errorf("arguments do not satisfy authoritative input schema: %w", err)
	}
	return nil
}

func knownExecutorClass(class toolcontract.ExecutorClass) bool {
	switch class {
	case toolcontract.ExecutorReadOnlyClosed,
		toolcontract.ExecutorReadOnlyOpen,
		toolcontract.ExecutorAdditiveClosed,
		toolcontract.ExecutorAdditiveClosedIdempotent,
		toolcontract.ExecutorAdditiveOpen,
		toolcontract.ExecutorAdditiveOpenIdempotent,
		toolcontract.ExecutorDestructiveClosed,
		toolcontract.ExecutorDestructiveClosedIdempotent,
		toolcontract.ExecutorDestructiveOpen,
		toolcontract.ExecutorDestructiveOpenIdempotent:
		return true
	default:
		return false
	}
}

func executionFailure(code, message string, outcome Outcome, retryable bool, cause error) *ExecutionError {
	return &ExecutionError{Code: code, Message: message, Outcome: outcome, Retryable: retryable, cause: cause}
}
