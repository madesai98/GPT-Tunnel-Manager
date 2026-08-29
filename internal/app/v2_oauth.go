package app

import (
	"context"
	"fmt"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/downstream"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routedlifecycle"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

// V2OAuthStatus is deliberately credential-free. It is safe to surface in the
// native application and distinguishes configuration from an established OAuth
// session.
type V2OAuthStatus struct {
	Configured bool
	Connected  bool
}

func (a *V2App) OAuthStatus(ctx context.Context, serverID string) V2OAuthStatus {
	_, configured := a.oauthServerEntry(serverID)
	return V2OAuthStatus{
		Configured: configured,
		Connected:  configured && downstream.OAuthConnected(ctx, a.secrets, serverID),
	}
}

// ConnectOAuth explicitly arms browser authorization and starts/restarts the
// downstream server. Normal application startup never arms OAuth and therefore
// never opens a browser on its own.
func (a *V2App) ConnectOAuth(ctx context.Context, serverID string) (routedlifecycle.Snapshot, error) {
	entry, ok := a.oauthServerEntry(serverID)
	if !ok {
		return routedlifecycle.Snapshot{}, fmt.Errorf("server %q is not configured for downstream OAuth", serverID)
	}
	if entry.Mode == v2config.ModeDisabled {
		return routedlifecycle.Snapshot{}, fmt.Errorf("server %q is disabled", serverID)
	}
	if err := downstream.ArmOAuthConnect(ctx, a.secrets, serverID); err != nil {
		return routedlifecycle.Snapshot{}, err
	}

	snapshot, err := a.oauthStartOrRestart(ctx, serverID)
	if err != nil {
		return snapshot, err
	}
	identity, err := downstream.OAuthSessionIdentity(ctx, a.secrets, serverID)
	if err != nil {
		return snapshot, fmt.Errorf("read OAuth account identity for %s: %w", serverID, err)
	}
	if _, err := a.state.SetOAuthCredentialIdentity(ctx, serverID, identity); err != nil {
		return snapshot, fmt.Errorf("record OAuth account identity for %s: %w", serverID, err)
	}
	return snapshot, nil
}

// ReconnectOAuth discards only the operational OAuth session and re-enters the
// explicit browser flow. The old routing identity remains until the replacement
// authorization succeeds, at which point SetOAuthCredentialIdentity detects
// an account/scope change and invalidates routing if necessary.
func (a *V2App) ReconnectOAuth(ctx context.Context, serverID string) (routedlifecycle.Snapshot, error) {
	if _, ok := a.oauthServerEntry(serverID); !ok {
		return routedlifecycle.Snapshot{}, fmt.Errorf("server %q is not configured for downstream OAuth", serverID)
	}
	if err := downstream.ClearOAuthSession(ctx, a.secrets, serverID); err != nil {
		return routedlifecycle.Snapshot{}, err
	}
	return a.ConnectOAuth(ctx, serverID)
}

func (a *V2App) DisconnectOAuth(ctx context.Context, serverID string) error {
	if _, ok := a.oauthServerEntry(serverID); !ok {
		return fmt.Errorf("server %q is not configured for downstream OAuth", serverID)
	}
	if err := downstream.ClearOAuthSession(ctx, a.secrets, serverID); err != nil {
		return err
	}
	_, err := a.state.ClearOAuthCredentialIdentity(ctx, serverID)
	return err
}

func (a *V2App) oauthServerEntry(serverID string) (v2config.ServerEntry, bool) {
	for _, entry := range a.Entries() {
		if entry.ID != serverID {
			continue
		}
		switch entry.Transport.Type {
		case v2config.TransportManagedHTTP:
			if entry.Transport.ManagedHTTP != nil && entry.Transport.ManagedHTTP.Auth.Mode == v2config.HTTPAuthOAuth && entry.Transport.ManagedHTTP.Auth.OAuth != nil {
				return entry, true
			}
		case v2config.TransportExternalHTTP:
			if entry.Transport.ExternalHTTP != nil && entry.Transport.ExternalHTTP.Auth.Mode == v2config.HTTPAuthOAuth && entry.Transport.ExternalHTTP.Auth.OAuth != nil {
				return entry, true
			}
		}
		return v2config.ServerEntry{}, false
	}
	return v2config.ServerEntry{}, false
}

func (a *V2App) oauthStartOrRestart(ctx context.Context, serverID string) (routedlifecycle.Snapshot, error) {
	for _, snapshot := range a.Snapshots() {
		if snapshot.ServerID == serverID {
			if snapshot.Running {
				return a.RestartServer(ctx, serverID)
			}
			break
		}
	}
	return a.StartServer(ctx, serverID)
}
