package mcpmanager

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/buildinfo"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/lifecycle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/servers"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const managerInstructions = "Manage GPT Tunnel Manager server lifecycle state."

type Server struct {
	registry  *servers.Registry
	mcp       *mcp.Server
	http      *http.Server
	ln        net.Listener
	url       string
	accepting atomic.Bool
}

type statusInput struct {
	ServerID       string `json:"server_id,omitempty" jsonschema:"Immutable configured server ID. Omit to list all configured servers."`
	WaitForReady   bool   `json:"wait_for_ready,omitempty" jsonschema:"Wait for the selected server to become ready, degraded, or stopped before returning."`
	TimeoutSeconds *int   `json:"timeout_seconds,omitempty" jsonschema:"Maximum number of seconds to wait when wait_for_ready is true. Allowed range is 1 through 60; defaults to 30."`
}

type mutationInput struct {
	ServerID string `json:"server_id" jsonschema:"Immutable configured server ID."`
}

type statusOutput struct {
	Servers []servers.Snapshot `json:"servers,omitempty" jsonschema:"Configured server lifecycle snapshots when no server_id was supplied."`
	Server  *servers.Snapshot  `json:"server,omitempty" jsonschema:"Lifecycle snapshot for the selected server."`
	Error   *toolError          `json:"error,omitempty" jsonschema:"Stable tool error details when the operation failed."`
}

type mutationOutput struct {
	Server *servers.Snapshot `json:"server,omitempty" jsonschema:"Lifecycle snapshot after the requested operation."`
	Error  *toolError         `json:"error,omitempty" jsonschema:"Stable tool error details when the operation failed."`
}

type toolError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func New(r *servers.Registry) *Server {
	s := &Server{registry: r}
	s.accepting.Store(true)
	s.mcp = mcp.NewServer(
		&mcp.Implementation{Name: "gpt-tunnel-manager", Version: buildinfo.Version},
		&mcp.ServerOptions{
			Instructions: managerInstructions,
			// The Manager MCP only exposes tools. Explicitly suppress the SDK's
			// historical default logging capability; tool capability metadata is
			// inferred from the registrations below.
			Capabilities: &mcp.ServerCapabilities{},
		},
	)
	s.registerTools()
	return s
}

func (s *Server) registerTools() {
	closedWorld := false
	nondestructive := false
	destructive := true

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "get_status",
		Title:       "Get manager status",
		Description: "Get GPT Tunnel Manager server lifecycle status. Optionally wait for one configured server to become ready.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Get manager status",
			ReadOnlyHint:    true,
			IdempotentHint:  true,
			DestructiveHint: &nondestructive,
			OpenWorldHint:   &closedWorld,
		},
	}, s.getStatus)

	mcp.AddTool(s.mcp, mutationTool(
		"start",
		"Start managed server",
		"Start one enabled Managed server entry by immutable server ID.",
		&nondestructive,
	), func(ctx context.Context, _ *mcp.CallToolRequest, input mutationInput) (*mcp.CallToolResult, mutationOutput, error) {
		return s.mutate(ctx, "start", input)
	})

	mcp.AddTool(s.mcp, mutationTool(
		"restart",
		"Restart managed server",
		"Restart one enabled Managed server entry by immutable server ID.",
		&destructive,
	), func(ctx context.Context, _ *mcp.CallToolRequest, input mutationInput) (*mcp.CallToolResult, mutationOutput, error) {
		return s.mutate(ctx, "restart", input)
	})

	mcp.AddTool(s.mcp, mutationTool(
		"shutdown",
		"Shut down managed server",
		"Stop one enabled Managed server entry by immutable server ID.",
		&destructive,
	), func(ctx context.Context, _ *mcp.CallToolRequest, input mutationInput) (*mcp.CallToolResult, mutationOutput, error) {
		return s.mutate(ctx, "shutdown", input)
	})
}

func mutationTool(name, title, description string, destructive *bool) *mcp.Tool {
	closedWorld := false
	return &mcp.Tool{
		Name:        name,
		Title:       title,
		Description: description,
		Annotations: &mcp.ToolAnnotations{
			Title:           title,
			ReadOnlyHint:    false,
			IdempotentHint:  true,
			DestructiveHint: destructive,
			OpenWorldHint:   &closedWorld,
		},
	}
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.ln = ln
	s.url = "http://" + ln.Addr().String() + "/mcp"
	s.accepting.Store(true)

	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return s.mcp },
		&mcp.StreamableHTTPOptions{
			Stateless:                    true,
			JSONResponse:                 true,
			MaxRequestBodyBytes:          2 << 20,
			PropagateRequestCancellation: true,
		},
	)

	mux := http.NewServeMux()
	mux.Handle("/mcp", rejectBrowserOrigins(mcpHandler))
	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = s.http.Serve(ln) }()
	return nil
}

func rejectBrowserOrigins(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The Manager endpoint is only for the locally-owned tunnel client.
		// Reject browser-originated requests so arbitrary sites cannot use
		// localhost as a bridge to enumerate or mutate lifecycle state.
		if r.Header.Get("Origin") != "" {
			http.Error(w, "browser origins are not accepted", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) URL() string { return s.url }

func (s *Server) Stop(ctx context.Context) error {
	s.accepting.Store(false)
	if s.http != nil {
		return s.http.Shutdown(ctx)
	}
	return nil
}

func (s *Server) getStatus(ctx context.Context, _ *mcp.CallToolRequest, input statusInput) (*mcp.CallToolResult, statusOutput, error) {
	if s.registry == nil {
		return statusFailure(errors.New("manager_registry_unavailable"))
	}
	if input.ServerID == "" {
		if input.WaitForReady {
			return statusFailure(errors.New("server_id_required_for_wait"))
		}
		return nil, statusOutput{Servers: s.registry.List()}, nil
	}

	sup, err := s.registry.Get(input.ServerID)
	if err != nil {
		return statusFailure(err)
	}
	snap := sup.Snapshot()
	if input.WaitForReady && !snap.Ready {
		timeout := 30
		if input.TimeoutSeconds != nil {
			if *input.TimeoutSeconds < 1 || *input.TimeoutSeconds > 60 {
				return statusFailure(errors.New("timeout_seconds_out_of_range"))
			}
			timeout = *input.TimeoutSeconds
		}
		waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
		snap, _ = sup.Wait(waitCtx, func(x servers.Snapshot) bool {
			return x.Ready || x.Observed == lifecycle.Degraded || x.Observed == lifecycle.Stopped
		})
	}
	return nil, statusOutput{Server: &snap}, nil
}

func (s *Server) mutate(ctx context.Context, operation string, input mutationInput) (*mcp.CallToolResult, mutationOutput, error) {
	if !s.accepting.Load() {
		return mutationFailure(errors.New("manager_shutting_down"))
	}
	if s.registry == nil {
		return mutationFailure(errors.New("manager_registry_unavailable"))
	}
	if input.ServerID == "" {
		return mutationFailure(errors.New("server_id_required"))
	}

	var (
		snap servers.Snapshot
		err  error
	)
	switch operation {
	case "start":
		snap, err = s.registry.Start(ctx, input.ServerID, servers.SourceMCP)
	case "restart":
		snap, err = s.registry.Restart(ctx, input.ServerID, servers.SourceMCP)
	case "shutdown":
		snap, err = s.registry.Shutdown(ctx, input.ServerID, servers.SourceMCP)
	default:
		err = errors.New("unknown_tool")
	}
	if err != nil {
		return mutationFailure(err)
	}
	return nil, mutationOutput{Server: &snap}, nil
}

func statusFailure(err error) (*mcp.CallToolResult, statusOutput, error) {
	return &mcp.CallToolResult{IsError: true}, statusOutput{Error: stableError(err)}, nil
}

func mutationFailure(err error) (*mcp.CallToolResult, mutationOutput, error) {
	return &mcp.CallToolResult{IsError: true}, mutationOutput{Error: stableError(err)}, nil
}

func stableError(err error) *toolError {
	code := "operation_failed"
	retryable := true
	switch {
	case errors.Is(err, servers.ErrNotFound):
		code = "server_not_found"
		retryable = false
	case errors.Is(err, servers.ErrDisabled):
		code = "server_disabled"
		retryable = false
	case errors.Is(err, servers.ErrModeConflict):
		code = "mode_not_mcp_controllable"
		retryable = false
	case err.Error() == "manager_shutting_down":
		code = "manager_shutting_down"
	case err.Error() == "server_id_required", err.Error() == "server_id_required_for_wait", err.Error() == "timeout_seconds_out_of_range":
		code = "invalid_request"
		retryable = false
	case err.Error() == "manager_registry_unavailable":
		code = "manager_unavailable"
	}
	return &toolError{Code: code, Message: err.Error(), Retryable: retryable}
}
