package downstream

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	gtmprocess "github.com/madesai98/GPT-Tunnel-Manager/internal/process"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	ErrDownstreamUnavailable = errors.New("downstream unavailable")
	ErrToolContractChanged   = errors.New("downstream tool contract changed")
)

type LogLine struct {
	ServerID string
	Stream   string
	Text     string
}

type OAuthHandlerProvider interface {
	Handler(context.Context, string, v2config.OAuthAuthConfig) (auth.OAuthHandler, error)
}

type OAuthHandlerProviderFunc func(context.Context, string, v2config.OAuthAuthConfig) (auth.OAuthHandler, error)

func (f OAuthHandlerProviderFunc) Handler(ctx context.Context, serverID string, cfg v2config.OAuthAuthConfig) (auth.OAuthHandler, error) {
	return f(ctx, serverID, cfg)
}

type Options struct {
	Secrets secrets.Store
	OAuth   OAuthHandlerProvider

	ClientOptions *mcp.ClientOptions
	HTTPTransport http.RoundTripper
	Log           func(LogLine)

	// OnToolContractChanged is diagnostic/invalidation plumbing for the later
	// catalog/router phases. It is called at most once per session after a
	// tools/list change notification or observed fingerprint drift.
	OnToolContractChanged func(serverID string)

	ManagedHTTPRetryInterval time.Duration
}

type Factory struct {
	secrets       secrets.Store
	oauth         OAuthHandlerProvider
	clientOptions *mcp.ClientOptions
	httpTransport http.RoundTripper
	log           func(LogLine)
	onToolChanged func(string)
	retryInterval time.Duration
}

func NewFactory(opts Options) (*Factory, error) {
	if opts.Secrets == nil {
		return nil, errors.New("downstream secret store is required")
	}
	retry := opts.ManagedHTTPRetryInterval
	if retry <= 0 {
		retry = 100 * time.Millisecond
	}
	return &Factory{
		secrets:       opts.Secrets,
		oauth:         opts.OAuth,
		clientOptions: opts.ClientOptions,
		httpTransport: opts.HTTPTransport,
		log:           opts.Log,
		onToolChanged: opts.OnToolContractChanged,
		retryInterval: retry,
	}, nil
}

type Session struct {
	serverID string
	sdk      *mcp.ClientSession
	initial  ToolSnapshot

	supportsToolListChanged bool
	toolContractChanged     *atomic.Bool
	notifyOnce              sync.Once
	onToolChanged           func(string)

	ownedProcess *gtmprocess.Managed
	processDone  <-chan struct{}
	shutdown     time.Duration

	closeOnce sync.Once
	closeErr  error
}

func (f *Factory) Connect(ctx context.Context, server v2config.ServerEntry) (*Session, error) {
	if err := v2config.ValidateServer(server); err != nil {
		return nil, fmt.Errorf("validate downstream server %s: %w", server.ID, err)
	}
	if server.Mode == v2config.ModeDisabled {
		return nil, fmt.Errorf("%w: server %s is disabled", ErrDownstreamUnavailable, server.ID)
	}

	changed := &atomic.Bool{}
	makeClient := func() (*mcp.Client, error) {
		opts := cloneClientOptions(f.clientOptions)
		previous := opts.ToolListChangedHandler
		opts.ToolListChangedHandler = func(callbackCtx context.Context, req *mcp.ToolListChangedRequest) {
			changed.Store(true)
			safeToolListChangedHandler(previous, callbackCtx, req)
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "gpt-tunnel-manager", Version: "v2"}, opts)
		if err := registerTaskMethods(client); err != nil {
			return nil, err
		}
		return client, nil
	}

	startupCtx, cancel := context.WithTimeout(ctx, server.StartupTimeout())
	defer cancel()

	var (
		sdkSession *mcp.ClientSession
		owned      *gtmprocess.Managed
		processDone <-chan struct{}
		stdio      *stdioTransport
		err        error
	)

	switch server.Transport.Type {
	case v2config.TransportStdio:
		client, clientErr := makeClient()
		if clientErr != nil {
			return nil, clientErr
		}
		stdio, err = f.newStdioTransport(startupCtx, server)
		if err != nil {
			return nil, err
		}
		sdkSession, err = client.Connect(startupCtx, stdio, nil)
		if err != nil {
			_ = stdio.Abort()
			return nil, fmt.Errorf("connect stdio MCP %s: %w", server.ID, err)
		}
		processDone = stdio.Done()

	case v2config.TransportManagedHTTP:
		owned, err = f.startManagedHTTP(startupCtx, server)
		if err != nil {
			return nil, err
		}
		processDone = owned.Done()
		sdkSession, err = f.connectManagedHTTP(startupCtx, server, owned, makeClient)
		if err != nil {
			stopCtx, stopCancel := context.WithTimeout(context.WithoutCancel(ctx), server.ShutdownTimeout())
			defer stopCancel()
			_ = owned.Stop(stopCtx, server.ShutdownTimeout())
			return nil, err
		}

	case v2config.TransportExternalHTTP:
		client, clientErr := makeClient()
		if clientErr != nil {
			return nil, clientErr
		}
		transport, transportErr := f.httpClientTransport(startupCtx, server)
		if transportErr != nil {
			return nil, transportErr
		}
		sdkSession, err = client.Connect(startupCtx, transport, nil)
		if err != nil {
			return nil, fmt.Errorf("connect external HTTP MCP %s: %w", server.ID, err)
		}

	default:
		return nil, fmt.Errorf("unsupported downstream transport %q", server.Transport.Type)
	}

	initial, err := SnapshotTools(startupCtx, sdkSession)
	if err != nil {
		_ = closeClientSessionBounded(sdkSession, server.ShutdownTimeout())
		if owned != nil {
			stopCtx, stopCancel := context.WithTimeout(context.WithoutCancel(ctx), server.ShutdownTimeout())
			_ = owned.Stop(stopCtx, server.ShutdownTimeout())
			stopCancel()
		}
		return nil, fmt.Errorf("list downstream tools for %s: %w", server.ID, err)
	}

	supportsChanged := false
	if initialized := sdkSession.InitializeResult(); initialized != nil && initialized.Capabilities != nil && initialized.Capabilities.Tools != nil {
		supportsChanged = initialized.Capabilities.Tools.ListChanged
	}

	s := &Session{
		serverID:                server.ID,
		sdk:                     sdkSession,
		initial:                 initial,
		supportsToolListChanged: supportsChanged,
		toolContractChanged:     changed,
		onToolChanged:           f.onToolChanged,
		ownedProcess:            owned,
		processDone:             processDone,
		shutdown:                server.ShutdownTimeout(),
	}
	return s, nil
}

func (s *Session) ServerID() string { return s.serverID }

func (s *Session) InitialTools() ToolSnapshot { return s.initial.Clone() }

func (s *Session) SupportsToolListChanged() bool { return s.supportsToolListChanged }

func (s *Session) ToolContractChanged() bool { return s.toolContractChanged.Load() }

func (s *Session) Done() <-chan struct{} { return s.processDone }

func (s *Session) ListTools(ctx context.Context) (ToolSnapshot, error) {
	if err := s.ensureAvailable(); err != nil {
		return ToolSnapshot{}, err
	}
	return SnapshotTools(ctx, s.sdk)
}

func (s *Session) RevalidateTools(ctx context.Context) error {
	if err := s.ensureAvailable(); err != nil {
		return err
	}
	current, err := SnapshotTools(ctx, s.sdk)
	if err != nil {
		return err
	}
	if current.Fingerprint != s.initial.Fingerprint {
		s.markToolContractChanged()
		return fmt.Errorf("%w: server %s tools/list fingerprint changed from %s to %s", ErrToolContractChanged, s.serverID, s.initial.Fingerprint, current.Fingerprint)
	}
	return nil
}

func (s *Session) CallTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	if params == nil || params.Name == "" {
		return nil, errors.New("downstream tool name is required")
	}
	if err := s.ensureAvailable(); err != nil {
		return nil, err
	}
	if s.toolContractChanged.Load() {
		s.markToolContractChanged()
		return nil, fmt.Errorf("%w: server %s advertised a tools/list change", ErrToolContractChanged, s.serverID)
	}
	if !s.supportsToolListChanged {
		if err := s.RevalidateTools(ctx); err != nil {
			return nil, err
		}
	}
	return s.sdk.CallTool(ctx, params)
}

func (s *Session) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		clientErr := closeClientSessionContext(ctx, s.sdk, s.shutdown)
		var processErr error
		if s.ownedProcess != nil {
			processErr = s.ownedProcess.Stop(ctx, s.shutdown)
		}
		s.closeErr = errors.Join(clientErr, processErr)
	})
	return s.closeErr
}

func (s *Session) ensureAvailable() error {
	if s.processDone == nil {
		return nil
	}
	select {
	case <-s.processDone:
		return fmt.Errorf("%w: owned process for server %s exited", ErrDownstreamUnavailable, s.serverID)
	default:
		return nil
	}
}

func (s *Session) markToolContractChanged() {
	s.toolContractChanged.Store(true)
	s.notifyOnce.Do(func() {
		if s.onToolChanged != nil {
			safeServerCallback(s.onToolChanged, s.serverID)
		}
	})
}

func cloneClientOptions(base *mcp.ClientOptions) *mcp.ClientOptions {
	if base == nil {
		return &mcp.ClientOptions{}
	}
	copy := *base
	return &copy
}

func safeToolListChangedHandler(fn func(context.Context, *mcp.ToolListChangedRequest), ctx context.Context, req *mcp.ToolListChangedRequest) {
	if fn == nil {
		return
	}
	defer func() { _ = recover() }()
	fn(ctx, req)
}

func safeServerCallback(fn func(string), serverID string) {
	defer func() { _ = recover() }()
	fn(serverID)
}

func closeClientSessionBounded(session *mcp.ClientSession, timeout time.Duration) error {
	if session == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	done := make(chan error, 1)
	go func() { done <- session.Close() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return errors.New("timed out closing downstream MCP session")
	}
}

func closeClientSessionContext(ctx context.Context, session *mcp.ClientSession, timeout time.Duration) error {
	if session == nil {
		return nil
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	done := make(chan error, 1)
	go func() { done <- session.Close() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("timed out closing downstream MCP session")
	}
}
