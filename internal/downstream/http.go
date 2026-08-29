package downstream

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	gtmprocess "github.com/madesai98/GPT-Tunnel-Manager/internal/process"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (f *Factory) startManagedHTTP(ctx context.Context, server v2config.ServerEntry) (*gtmprocess.Managed, error) {
	cfg := server.Transport.ManagedHTTP
	if cfg == nil {
		return nil, errors.New("managed HTTP transport configuration is missing")
	}
	env, redactions, err := f.resolveEnvironment(ctx, server)
	if err != nil {
		return nil, err
	}
	managed, err := gtmprocess.Start(gtmprocess.Spec{
		Executable: cfg.Launch.Executable,
		Args:       cfg.Launch.Args,
		Dir:        cfg.Launch.WorkingDirectory,
		Env:        env,
	}, func(line gtmprocess.Line) {
		f.emitLog(server.ID, line.Stream, line.Text, redactions)
	})
	if err != nil {
		return nil, fmt.Errorf("start managed HTTP MCP %s: %w", server.ID, err)
	}
	return managed, nil
}

func (f *Factory) connectManagedHTTP(ctx context.Context, server v2config.ServerEntry, owned *gtmprocess.Managed, makeClient func() (*mcp.Client, error)) (*mcp.ClientSession, error) {
	endpoint := server.Transport.ManagedHTTP.URL
	if err := waitForHTTPEndpoint(ctx, endpoint, owned.Done(), f.retryInterval); err != nil {
		return nil, fmt.Errorf("wait for managed HTTP MCP %s: %w", server.ID, err)
	}
	client, err := makeClient()
	if err != nil {
		return nil, err
	}
	transport, err := f.httpClientTransport(ctx, server)
	if err != nil {
		return nil, err
	}
	session, err := client.Connect(ctx, wrapTaskAwareTransport(transport), nil)
	if err != nil {
		return nil, fmt.Errorf("connect managed HTTP MCP %s: %w", server.ID, err)
	}
	return session, nil
}

func (f *Factory) httpClientTransport(ctx context.Context, server v2config.ServerEntry) (*mcp.StreamableClientTransport, error) {
	var endpoint string
	var cfg v2config.HTTPAuthConfig
	switch server.Transport.Type {
	case v2config.TransportManagedHTTP:
		if server.Transport.ManagedHTTP == nil {
			return nil, errors.New("managed HTTP transport configuration is missing")
		}
		endpoint = server.Transport.ManagedHTTP.URL
		cfg = server.Transport.ManagedHTTP.Auth
	case v2config.TransportExternalHTTP:
		if server.Transport.ExternalHTTP == nil {
			return nil, errors.New("external HTTP transport configuration is missing")
		}
		endpoint = server.Transport.ExternalHTTP.URL
		cfg = server.Transport.ExternalHTTP.Auth
	default:
		return nil, fmt.Errorf("transport %q is not HTTP", server.Transport.Type)
	}

	transport := &mcp.StreamableClientTransport{Endpoint: endpoint}
	base := f.httpTransport
	if base == nil {
		base = http.DefaultTransport
	}

	switch cfg.Mode {
	case v2config.HTTPAuthNone:
		if f.httpTransport != nil {
			transport.HTTPClient = &http.Client{Transport: base}
		}

	case v2config.HTTPAuthStatic:
		if cfg.Static == nil {
			return nil, errors.New("static HTTP auth configuration is missing")
		}
		secret, err := f.secrets.Get(ctx, cfg.Static.SecretRef)
		if err != nil {
			return nil, fmt.Errorf("resolve static HTTP credential for %s: %w", server.ID, err)
		}
		value := string(secret)
		if cfg.Static.Scheme != "" {
			value = cfg.Static.Scheme + " " + value
		}
		transport.HTTPClient = &http.Client{Transport: staticHeaderRoundTripper{
			base:  base,
			name:  cfg.Static.HeaderName,
			value: value,
		}}

	case v2config.HTTPAuthOAuth:
		if cfg.OAuth == nil {
			return nil, errors.New("OAuth HTTP auth configuration is missing")
		}
		if f.oauth == nil {
			return nil, fmt.Errorf("OAuth handler provider is required for server %s", server.ID)
		}
		handler, err := f.oauth.Handler(ctx, server.ID, *cfg.OAuth)
		if err != nil {
			return nil, fmt.Errorf("create OAuth handler for %s: %w", server.ID, err)
		}
		if handler == nil {
			return nil, fmt.Errorf("OAuth handler provider returned nil for server %s", server.ID)
		}
		transport.OAuthHandler = handler
		if f.httpTransport != nil {
			transport.HTTPClient = &http.Client{Transport: base}
		}

	default:
		return nil, fmt.Errorf("unsupported HTTP auth mode %q", cfg.Mode)
	}
	return transport, nil
}

type staticHeaderRoundTripper struct {
	base  http.RoundTripper
	name  string
	value string
}

func (r staticHeaderRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set(r.name, r.value)
	return r.base.RoundTrip(clone)
}

func waitForHTTPEndpoint(ctx context.Context, rawURL string, processDone <-chan struct{}, retry time.Duration) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	address := net.JoinHostPort(host, port)
	if retry <= 0 {
		retry = 100 * time.Millisecond
	}
	dialer := net.Dialer{Timeout: minDuration(retry, 500*time.Millisecond)}
	for {
		connection, dialErr := dialer.DialContext(ctx, "tcp", address)
		if dialErr == nil {
			_ = connection.Close()
			return nil
		}
		if processDone != nil {
			select {
			case <-processDone:
				return ErrDownstreamUnavailable
			default:
			}
		}
		timer := time.NewTimer(retry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-processDone:
			timer.Stop()
			return ErrDownstreamUnavailable
		case <-timer.C:
		}
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// OAuthSecretNamespace is the internal secret-store namespace reserved for a
// downstream server's OAuth state. Interactive UI code may persist tokens and
// authorization state beneath this prefix; those values never belong in config
// JSON or routing/index text.
func OAuthSecretNamespace(serverID string) string {
	return "secret://servers/" + serverID + "/oauth/"
}
