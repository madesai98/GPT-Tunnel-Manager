package mcpmanager

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/buildinfo"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/discovery"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/executionhandle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/executionrouter"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/indexing"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routedlifecycle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingprefs"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	managerMCPPath       = "/mcp"
	legacySessionTimeout = 30 * time.Minute
)

const fullManagerInstructions = "Maintain the explicit Tool Catalog, search and inspect authoritative downstream tool contracts, manage routing preferences, and execute only through the permission-class tool authorized by get_tool."

type V2ServerOptions struct {
	Manager v2config.ManagerConfig

	Catalog     *catalog.Catalog
	Secrets     secrets.Store
	Lifecycle   *routedlifecycle.Service
	Indexing    *indexing.Service
	Discovery   *discovery.Service
	Preferences *routingprefs.Store

	RoutingState executionrouter.RoutingState
	Handles      *executionhandle.Manager
	Execution    executionrouter.Options
}

// V2Server is the canonical Phase 10 one-URL Manager MCP endpoint. It is kept
// separate from the old lifecycle-oriented Server constructor until Phase 11
// moves native bootstrap to the new topology and Phase 12 removes the v1 seam.
type V2Server struct {
	config       v2config.ManagerConfig
	mcp          *mcp.Server
	continuation *continuationService
	handler      http.Handler

	mu         sync.Mutex
	listener   net.Listener
	httpServer *http.Server
	url        string
}

func NewV2Server(ctx context.Context, opts V2ServerOptions) (*V2Server, error) {
	if opts.Manager.LocalManager.Port < 1 || opts.Manager.LocalManager.Port > 65535 {
		return nil, fmt.Errorf("local Manager MCP port must be between 1 and 65535")
	}
	if opts.Catalog == nil || opts.Secrets == nil || opts.Lifecycle == nil || opts.Indexing == nil || opts.Discovery == nil || opts.Preferences == nil || opts.RoutingState == nil || opts.Handles == nil {
		return nil, errors.New("canonical Manager MCP dependencies are incomplete")
	}
	capability, err := ensureManagerCapability(ctx, opts.Secrets)
	if err != nil {
		return nil, err
	}
	continuation, err := newContinuationService(ctx, opts.Catalog, opts.Lifecycle, capability)
	if err != nil {
		return nil, err
	}
	execution, err := executionrouter.NewService(
		opts.Catalog,
		opts.RoutingState,
		opts.Handles,
		newContinuationSessionProvider(opts.Lifecycle, continuation),
		opts.Execution,
	)
	if err != nil {
		continuation.Close()
		return nil, err
	}

	logical := mcp.NewServer(
		&mcp.Implementation{Name: "gpt-tunnel-manager", Version: buildinfo.Version},
		&mcp.ServerOptions{Instructions: fullManagerInstructions, Capabilities: &mcp.ServerCapabilities{}},
	)
	registerV2IndexTools(logical, opts.Indexing)
	registerV2DiscoveryTools(logical, opts.Discovery)
	registerV2PreferenceTools(logical, opts.Preferences)
	registerV2ExecutionTools(logical, execution)
	if err := registerV2ContinuationProtocol(logical, continuation); err != nil {
		continuation.Close()
		return nil, err
	}

	modern := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return logical }, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		PropagateRequestCancellation: true,
	})
	legacy := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return logical }, &mcp.StreamableHTTPOptions{
		JSONResponse:    true,
		SessionTimeout:  legacySessionTimeout,
	})
	hybrid := hybridV2Handler(modern, legacy)
	wire := taskWireResponseHandler(hybrid)
	secured := localManagerSecurityHandler(wire, opts.Manager.LocalManager.AccessProtectionEnabled, capability)
	mux := http.NewServeMux()
	mux.Handle(managerMCPPath, secured)

	return &V2Server{
		config:       opts.Manager,
		mcp:          logical,
		continuation: continuation,
		handler:      mux,
		url:          "http://127.0.0.1:" + strconv.Itoa(opts.Manager.LocalManager.Port) + managerMCPPath,
	}, nil
}

func (s *V2Server) URL() string {
	if s == nil {
		return ""
	}
	return s.url
}

func (s *V2Server) Handler() http.Handler {
	if s == nil {
		return nil
	}
	return s.handler
}

func (s *V2Server) Start() error {
	if s == nil {
		return errors.New("nil v2 Manager MCP server")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return nil
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(s.config.LocalManager.Port))
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return fmt.Errorf("listen on local Manager MCP endpoint %s: %w", address, err)
	}
	httpServer := &http.Server{Handler: s.handler, ReadHeaderTimeout: 10 * time.Second}
	s.listener = listener
	s.httpServer = httpServer
	go func() {
		_ = httpServer.Serve(listener)
	}()
	return nil
}

func (s *V2Server) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	httpServer := s.httpServer
	s.httpServer = nil
	s.listener = nil
	s.mu.Unlock()
	var err error
	if httpServer != nil {
		err = httpServer.Shutdown(ctx)
	}
	if s.continuation != nil {
		s.continuation.Close()
	}
	return err
}

func ensureManagerCapability(ctx context.Context, store secrets.Store) ([]byte, error) {
	value, err := store.Get(ctx, v2config.LocalManagerCapabilitySecretRef)
	if err == nil {
		if len(value) < 32 {
			return nil, errors.New("stored local Manager capability is too short")
		}
		return append([]byte(nil), value...), nil
	}
	if !errors.Is(err, secrets.ErrNotFound) {
		return nil, err
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, fmt.Errorf("generate local Manager capability: %w", err)
	}
	value = []byte(base64.RawURLEncoding.EncodeToString(random[:]))
	if err := store.Put(ctx, v2config.LocalManagerCapabilitySecretRef, value); err != nil {
		return nil, err
	}
	return value, nil
}

func localManagerSecurityHandler(next http.Handler, enabled bool, capability []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		// Browser-originated access is unconditionally rejected even when local
		// capability protection has been explicitly disabled in v2 config.
		if strings.TrimSpace(request.Header.Get("Origin")) != "" {
			http.Error(w, "browser Origin requests are not permitted", http.StatusForbidden)
			return
		}
		if enabled {
			authorization := request.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(authorization, prefix) {
				http.Error(w, "local Manager capability required", http.StatusUnauthorized)
				return
			}
			provided := []byte(strings.TrimSpace(strings.TrimPrefix(authorization, prefix)))
			if len(provided) != len(capability) || subtle.ConstantTimeCompare(provided, capability) != 1 {
				http.Error(w, "local Manager capability required", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, request)
	})
}

func hybridV2Handler(modern, legacy http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body []byte
		if request.Method == http.MethodPost && request.Body != nil {
			var err error
			body, err = io.ReadAll(request.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_ = request.Body.Close()
			request.Body = io.NopCloser(bytes.NewReader(body))
		}
		isDiscover := bytes.Contains(body, []byte(`"method":"server/discover"`))
		if request.Header.Get("Mcp-Protocol-Version") >= modernProtocolVersion || isDiscover {
			modern.ServeHTTP(w, request)
			return
		}
		legacy.ServeHTTP(w, request)
	})
}

func taskWireResponseHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			next.ServeHTTP(w, request)
			return
		}
		buffer := newBufferedHTTPResponse()
		next.ServeHTTP(buffer, request)
		body := buffer.body.Bytes()
		if rewritten, ok := rewriteTaskWireResponse(body); ok {
			body = rewritten
		}
		copyHTTPHeader(w.Header(), buffer.header)
		status := buffer.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
}

func rewriteTaskWireResponse(body []byte) ([]byte, bool) {
	var envelope map[string]json.RawMessage
	if len(body) == 0 || json.Unmarshal(body, &envelope) != nil || len(envelope["result"]) == 0 {
		return nil, false
	}
	var result mcp.CallToolResult
	if json.Unmarshal(envelope["result"], &result) != nil {
		return nil, false
	}
	task, ok, err := downstream.TaskFromCallResult(&result)
	if err != nil || !ok || task == nil {
		return nil, false
	}
	rawTask, err := json.Marshal(task)
	if err != nil {
		return nil, false
	}
	envelope["result"] = rawTask
	rewritten, err := json.Marshal(envelope)
	if err != nil {
		return nil, false
	}
	return rewritten, true
}

type bufferedHTTPResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newBufferedHTTPResponse() *bufferedHTTPResponse {
	return &bufferedHTTPResponse{header: make(http.Header)}
}

func (w *bufferedHTTPResponse) Header() http.Header { return w.header }
func (w *bufferedHTTPResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *bufferedHTTPResponse) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(body)
}
func (w *bufferedHTTPResponse) Flush() {}

func copyHTTPHeader(dst, src http.Header) {
	for key := range dst {
		dst.Del(key)
	}
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
