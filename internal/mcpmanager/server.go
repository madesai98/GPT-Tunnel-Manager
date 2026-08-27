package mcpmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/buildinfo"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/lifecycle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/servers"
)

type Server struct {
	registry  *servers.Registry
	http      *http.Server
	ln        net.Listener
	url       string
	accepting atomic.Bool
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func New(r *servers.Registry) *Server {
	s := &Server{registry: r}
	s.accepting.Store(true)
	return s
}

func (s *Server) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.ln = ln
	s.url = "http://" + ln.Addr().String() + "/mcp"
	s.accepting.Store(true)
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handle)
	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = s.http.Serve(ln) }()
	return nil
}

func (s *Server) URL() string { return s.url }

func (s *Server) Stop(ctx context.Context) error {
	s.accepting.Store(false)
	if s.http != nil {
		return s.http.Shutdown(ctx)
	}
	return nil
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	// This endpoint is for the locally-owned tunnel-client, not web pages.
	// Reject browser-originated traffic so arbitrary sites cannot use localhost
	// as a bridge to enumerate or mutate configured server lifecycle state.
	if r.Header.Get("Origin") != "" {
		http.Error(w, "browser origins are not accepted", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.write(w, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	if req.Method == "notifications/initialized" || strings.HasPrefix(req.Method, "notifications/") {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	res := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", fmt.Sprintf("gtm-%d", time.Now().UnixNano()))
		res.Result = map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "gpt-tunnel-manager", "version": buildinfo.Version},
		}
	case "tools/list":
		res.Result = map[string]any{"tools": tools()}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := decodeStrict(req.Params, &p); err != nil {
			res.Error = &rpcError{Code: -32602, Message: "invalid params"}
			break
		}
		out, err := s.call(r.Context(), p.Name, p.Arguments)
		if err != nil {
			res.Result = toolResult(map[string]any{"ok": false, "error": stableError(err)}, true)
		} else {
			res.Result = toolResult(out, false)
		}
	default:
		res.Error = &rpcError{Code: -32601, Message: "method not found"}
	}
	s.write(w, res)
}

func decodeStrict(raw []byte, v any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func (s *Server) write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func tools() []map[string]any {
	return []map[string]any{
		{
			"name":        "get_status",
			"description": "Get GPT Tunnel Manager server lifecycle status. Optionally wait for one configured server to become ready.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server_id":       map[string]any{"type": "string"},
					"wait_for_ready":  map[string]any{"type": "boolean"},
					"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 60},
				},
				"additionalProperties": false,
			},
		},
		{"name": "start", "description": "Start one enabled Managed server entry by immutable server ID.", "inputSchema": mutationSchema()},
		{"name": "restart", "description": "Restart one enabled Managed server entry by immutable server ID.", "inputSchema": mutationSchema()},
		{"name": "shutdown", "description": "Stop one enabled Managed server entry by immutable server ID.", "inputSchema": mutationSchema()},
	}
}

func mutationSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"server_id": map[string]any{"type": "string"}},
		"required":             []string{"server_id"},
		"additionalProperties": false,
	}
}

func toolResult(v any, isErr bool) map[string]any {
	b, _ := json.Marshal(v)
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(b)}},
		"structuredContent": v,
		"isError":           isErr,
	}
}

func (s *Server) call(ctx context.Context, name string, args json.RawMessage) (any, error) {
	if !s.accepting.Load() && name != "get_status" {
		return nil, errors.New("manager_shutting_down")
	}
	switch name {
	case "get_status":
		var a struct {
			ServerID string `json:"server_id"`
			Wait     bool   `json:"wait_for_ready"`
			Timeout  int    `json:"timeout_seconds"`
		}
		if err := decodeStrict(args, &a); err != nil {
			return nil, err
		}
		if a.ServerID == "" {
			if a.Wait {
				return nil, errors.New("server_id_required_for_wait")
			}
			return map[string]any{"servers": s.registry.List()}, nil
		}
		sup, err := s.registry.Get(a.ServerID)
		if err != nil {
			return nil, err
		}
		snap := sup.Snapshot()
		if a.Wait && !snap.Ready {
			n := a.Timeout
			if n <= 0 {
				n = 30
			}
			if n > 60 {
				n = 60
			}
			waitCtx, cancel := context.WithTimeout(ctx, time.Duration(n)*time.Second)
			defer cancel()
			snap, _ = sup.Wait(waitCtx, func(x servers.Snapshot) bool {
				return x.Ready || x.Observed == lifecycle.Degraded || x.Observed == lifecycle.Stopped
			})
		}
		return map[string]any{"server": snap}, nil
	case "start", "restart", "shutdown":
		var a struct {
			ServerID string `json:"server_id"`
		}
		if err := decodeStrict(args, &a); err != nil || a.ServerID == "" {
			return nil, errors.New("server_id_required")
		}
		var (
			snap servers.Snapshot
			err  error
		)
		switch name {
		case "start":
			snap, err = s.registry.Start(ctx, a.ServerID, servers.SourceMCP)
		case "restart":
			snap, err = s.registry.Restart(ctx, a.ServerID, servers.SourceMCP)
		case "shutdown":
			snap, err = s.registry.Shutdown(ctx, a.ServerID, servers.SourceMCP)
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"server": snap}, nil
	default:
		return nil, errors.New("unknown_tool")
	}
}

func stableError(err error) map[string]any {
	code := "operation_failed"
	switch {
	case errors.Is(err, servers.ErrNotFound):
		code = "server_not_found"
	case errors.Is(err, servers.ErrDisabled):
		code = "server_disabled"
	case errors.Is(err, servers.ErrModeConflict):
		code = "mode_not_mcp_controllable"
	case err.Error() == "manager_shutting_down":
		code = "manager_shutting_down"
	}
	return map[string]any{"code": code, "message": err.Error(), "retryable": code == "operation_failed"}
}
