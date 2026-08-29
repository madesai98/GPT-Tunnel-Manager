package downstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
)

func TestCheckpointBListAndCallAllTransports(t *testing.T) {
	ctx := context.Background()
	secretStore := newTestSecretStore()
	factory, err := NewFactory(Options{Secrets: secretStore})
	if err != nil {
		t.Fatal(err)
	}
	binary := buildCompatTestServer(t)

	stdio := v2config.ServerEntry{
		ID:   "srv_10000000000000000000000000000001",
		Name: "stdio",
		Mode: v2config.ModeManaged,
		Transport: v2config.TransportConfig{
			Type:  v2config.TransportStdio,
			Stdio: &v2config.StdioTransport{Executable: binary},
		},
		Runtime: testRuntime(),
	}
	stdioSession, err := factory.Connect(ctx, stdio)
	if err != nil {
		t.Fatalf("connect stdio: %v", err)
	}
	stdioFingerprint := assertEchoCall(t, stdioSession, "stdio-ok")
	stdioDone := stdioSession.Done()
	if stdioDone == nil {
		t.Fatal("stdio session must expose its owned process lifetime")
	}
	if err := stdioSession.Close(ctx); err != nil {
		t.Fatalf("close stdio: %v", err)
	}
	assertClosed(t, stdioDone, "stdio process")

	address := freeTCPAddress(t)
	managed := v2config.ServerEntry{
		ID:   "srv_10000000000000000000000000000002",
		Name: "managed-http",
		Mode: v2config.ModeManaged,
		Transport: v2config.TransportConfig{
			Type: v2config.TransportManagedHTTP,
			ManagedHTTP: &v2config.ManagedHTTPTransport{
				URL:  "http://" + address + "/mcp",
				Auth: v2config.HTTPAuthConfig{Mode: v2config.HTTPAuthNone},
				Launch: v2config.LaunchConfig{
					Executable: binary,
					Args:       []string{"-http", address},
				},
			},
		},
		Runtime: testRuntime(),
	}
	managedSession, err := factory.Connect(ctx, managed)
	if err != nil {
		t.Fatalf("connect managed HTTP: %v", err)
	}
	managedFingerprint := assertEchoCall(t, managedSession, "managed-ok")
	managedDone := managedSession.Done()
	if managedDone == nil {
		t.Fatal("managed HTTP session must expose its owned process lifetime")
	}
	if err := managedSession.Close(ctx); err != nil {
		t.Fatalf("close managed HTTP: %v", err)
	}
	assertClosed(t, managedDone, "managed HTTP process")

	externalHTTP := httptest.NewServer(newEchoMCPHandler())
	defer externalHTTP.Close()
	external := v2config.ServerEntry{
		ID:   "srv_10000000000000000000000000000003",
		Name: "external-http",
		Mode: v2config.ModeAlwaysOn,
		Transport: v2config.TransportConfig{
			Type: v2config.TransportExternalHTTP,
			ExternalHTTP: &v2config.ExternalHTTPTransport{
				URL:  externalHTTP.URL,
				Auth: v2config.HTTPAuthConfig{Mode: v2config.HTTPAuthNone},
			},
		},
		Runtime: testRuntime(),
	}
	externalSession, err := factory.Connect(ctx, external)
	if err != nil {
		t.Fatalf("connect external HTTP: %v", err)
	}
	externalFingerprint := assertEchoCall(t, externalSession, "external-ok")
	if externalSession.Done() != nil {
		t.Fatal("external HTTP session must not claim ownership of a remote process")
	}
	if err := externalSession.Close(ctx); err != nil {
		t.Fatalf("close external HTTP: %v", err)
	}
	response, err := http.Get(externalHTTP.URL)
	if err != nil {
		t.Fatalf("external HTTP service was no longer reachable after session close: %v", err)
	}
	_ = response.Body.Close()

	if stdioFingerprint != managedFingerprint || stdioFingerprint != externalFingerprint {
		t.Fatalf("identical downstream contracts produced different fingerprints: stdio=%s managed=%s external=%s", stdioFingerprint, managedFingerprint, externalFingerprint)
	}
}

func TestExternalHTTPStaticAuthUsesSecretStore(t *testing.T) {
	const rawSecret = "static-secret-value"
	const secretRef = "secret://servers/static-test/api-key"
	secretStore := newTestSecretStore()
	if err := secretStore.Put(context.Background(), secretRef, []byte(rawSecret)); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(requireAuthorization("Bearer "+rawSecret, newEchoMCPHandler()))
	defer server.Close()

	factory, err := NewFactory(Options{Secrets: secretStore})
	if err != nil {
		t.Fatal(err)
	}
	entry := externalHTTPServer("srv_20000000000000000000000000000001", server.URL)
	entry.Transport.ExternalHTTP.Auth = v2config.HTTPAuthConfig{
		Mode: v2config.HTTPAuthStatic,
		Static: &v2config.StaticAuthConfig{
			HeaderName: "Authorization",
			Scheme:     "Bearer",
			SecretRef:  secretRef,
		},
	}
	session, err := factory.Connect(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	assertEchoCall(t, session, "static-auth-ok")
}

func TestExternalHTTPOAuthAuthorizeRetryIsServerScoped(t *testing.T) {
	const token = "oauth-token-value"
	server := httptest.NewServer(requireAuthorization("Bearer "+token, newEchoMCPHandler()))
	defer server.Close()

	handler := &testOAuthHandler{token: token}
	var providerServerID string
	var providerScopes []string
	provider := OAuthHandlerProviderFunc(func(_ context.Context, serverID string, cfg v2config.OAuthAuthConfig) (auth.OAuthHandler, error) {
		providerServerID = serverID
		providerScopes = append([]string(nil), cfg.Scopes...)
		return handler, nil
	})
	factory, err := NewFactory(Options{Secrets: newTestSecretStore(), OAuth: provider})
	if err != nil {
		t.Fatal(err)
	}
	entry := externalHTTPServer("srv_20000000000000000000000000000002", server.URL)
	entry.Transport.ExternalHTTP.Auth = v2config.HTTPAuthConfig{
		Mode:  v2config.HTTPAuthOAuth,
		OAuth: &v2config.OAuthAuthConfig{Scopes: []string{"tools.read"}},
	}
	session, err := factory.Connect(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	assertEchoCall(t, session, "oauth-ok")
	if handler.authorizeCalls.Load() != 1 {
		t.Fatalf("OAuth authorize calls = %d, want 1", handler.authorizeCalls.Load())
	}
	if providerServerID != entry.ID || len(providerScopes) != 1 || providerScopes[0] != "tools.read" {
		t.Fatalf("OAuth provider did not receive server-scoped config: server=%q scopes=%v", providerServerID, providerScopes)
	}
	if got := OAuthSecretNamespace(entry.ID); got != "secret://servers/"+entry.ID+"/oauth/" {
		t.Fatalf("OAuth secret namespace = %q", got)
	} else if err := secrets.ValidateRef(got + "token"); err != nil {
		t.Fatalf("OAuth secret namespace is not a valid secret ref prefix: %v", err)
	}
}

func TestEnvironmentSecretResolutionAndLogRedaction(t *testing.T) {
	const secretRef = "secret://servers/env-test/token"
	const secretValue = "do-not-log-this-secret"
	store := newTestSecretStore()
	if err := store.Put(context.Background(), secretRef, []byte(secretValue)); err != nil {
		t.Fatal(err)
	}
	var logged LogLine
	factory, err := NewFactory(Options{
		Secrets: store,
		Log: func(line LogLine) {
			logged = line
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	entry := v2config.ServerEntry{
		ID:   "srv_30000000000000000000000000000001",
		Name: "env",
		Mode: v2config.ModeManaged,
		Environment: v2config.EnvironmentConfig{
			Values:     map[string]string{"PLAIN": "value"},
			SecretRefs: map[string]string{"TOKEN": secretRef},
		},
	}
	env, redactions, err := factory.resolveEnvironment(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "PLAIN=value") || !strings.Contains(joined, "TOKEN="+secretValue) {
		t.Fatalf("resolved environment is incomplete: %v", env)
	}
	factory.emitLog(entry.ID, "stderr", "credential="+secretValue, redactions)
	if strings.Contains(logged.Text, secretValue) || logged.Text != "credential=[REDACTED]" {
		t.Fatalf("secret was not redacted from child log: %q", logged.Text)
	}
}

func TestCallFailsClosedAfterToolListChangeSignal(t *testing.T) {
	s := &Session{
		serverID:            "srv_40000000000000000000000000000001",
		toolContractChanged: &atomic.Bool{},
	}
	s.toolContractChanged.Store(true)
	_, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: "echo"})
	if !errors.Is(err, ErrToolContractChanged) {
		t.Fatalf("CallTool error = %v, want ErrToolContractChanged", err)
	}
}

func assertEchoCall(t *testing.T, session *Session, text string) string {
	t.Helper()
	snapshot := session.InitialTools()
	if len(snapshot.Tools) != 1 || snapshot.Tools[0].Name != "echo" {
		t.Fatalf("unexpected tools/list snapshot: %#v", snapshot.Tools)
	}
	if !strings.HasPrefix(snapshot.Fingerprint, ToolFingerprintAlgorithm+":") {
		t.Fatalf("unexpected tool fingerprint %q", snapshot.Fingerprint)
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{"text": text},
	})
	if err != nil {
		t.Fatalf("CallTool(echo): %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("echo content length = %d", len(result.Content))
	}
	content, ok := result.Content[0].(*mcp.TextContent)
	if !ok || content.Text != text {
		t.Fatalf("echo result = %#v, want %q", result.Content[0], text)
	}
	return snapshot.Fingerprint
}

func newEchoMCPHandler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "gtm-v2-downstream-test", Version: "phase3"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "echo",
		Description: "Echo a text argument.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
	}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: args.Text}}}, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
}

func requireAuthorization(want string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != want {
			w.Header().Set("WWW-Authenticate", `Bearer realm="test"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, request)
	})
}

func externalHTTPServer(id, endpoint string) v2config.ServerEntry {
	return v2config.ServerEntry{
		ID:   id,
		Name: "external",
		Mode: v2config.ModeAlwaysOn,
		Transport: v2config.TransportConfig{
			Type: v2config.TransportExternalHTTP,
			ExternalHTTP: &v2config.ExternalHTTPTransport{
				URL:  endpoint,
				Auth: v2config.HTTPAuthConfig{Mode: v2config.HTTPAuthNone},
			},
		},
		Runtime: testRuntime(),
	}
}

func testRuntime() v2config.RuntimeConfig {
	return v2config.RuntimeConfig{StartupTimeoutSeconds: 10, ShutdownTimeoutSeconds: 3}
}

func freeTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func assertClosed(t *testing.T, done <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s was not terminated", name)
	}
}

func buildCompatTestServer(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	name := "downstream-testserver"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	command := exec.Command("go", "build", "-o", binary, "./internal/mcpcompat/testserver")
	command.Dir = repoRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build compatibility test server: %v\n%s", err, output)
	}
	return binary
}

type testSecretStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func newTestSecretStore() *testSecretStore { return &testSecretStore{values: make(map[string][]byte)} }

func (s *testSecretStore) Put(_ context.Context, ref string, value []byte) error {
	if err := secrets.ValidateRef(ref); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[ref] = append([]byte(nil), value...)
	return nil
}

func (s *testSecretStore) Get(_ context.Context, ref string) ([]byte, error) {
	if err := secrets.ValidateRef(ref); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[ref]
	if !ok {
		return nil, secrets.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *testSecretStore) Delete(_ context.Context, ref string) error {
	if err := secrets.ValidateRef(ref); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, ref)
	return nil
}

type testOAuthHandler struct {
	token          string
	authorized     atomic.Bool
	authorizeCalls atomic.Int32
}

var _ auth.OAuthHandler = (*testOAuthHandler)(nil)

func (h *testOAuthHandler) TokenSource(context.Context) (oauth2.TokenSource, error) {
	if !h.authorized.Load() {
		return nil, nil
	}
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: h.token, TokenType: "Bearer"}), nil
}

func (h *testOAuthHandler) Authorize(_ context.Context, _ *http.Request, response *http.Response) error {
	h.authorizeCalls.Add(1)
	h.authorized.Store(true)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	return nil
}

func ExampleOAuthSecretNamespace() {
	fmt.Println(OAuthSecretNamespace("srv_0123456789abcdef0123456789abcdef"))
	// Output: secret://servers/srv_0123456789abcdef0123456789abcdef/oauth/
}
