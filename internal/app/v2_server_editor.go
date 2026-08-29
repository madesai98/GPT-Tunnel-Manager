package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

// NewServerID generates the opaque v2 identifier used by the native editor.
// Users never need to type or understand this value.
func NewServerID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "srv_" + hex.EncodeToString(raw[:]), nil
}

// NewServerEntry creates a native-editor entry with safe runtime defaults and
// the requested transport shape. Transport-specific required fields remain for
// the editor to fill before SaveServer is called.
func NewServerEntry(name string, mode v2config.ServerMode, transport v2config.TransportType) (v2config.ServerEntry, error) {
	id, err := NewServerID()
	if err != nil {
		return v2config.ServerEntry{}, err
	}
	entry := v2config.ServerEntry{
		ID: id,
		Name: strings.TrimSpace(name),
		Mode: mode,
		Runtime: v2config.RuntimeConfig{StartupTimeoutSeconds: 30, ShutdownTimeoutSeconds: 10},
	}
	switch transport {
	case v2config.TransportStdio:
		entry.Transport = v2config.TransportConfig{Type: transport, Stdio: &v2config.StdioTransport{}}
	case v2config.TransportManagedHTTP:
		entry.Transport = v2config.TransportConfig{Type: transport, ManagedHTTP: &v2config.ManagedHTTPTransport{Auth: v2config.HTTPAuthConfig{Mode: v2config.HTTPAuthNone}}}
	case v2config.TransportExternalHTTP:
		entry.Transport = v2config.TransportConfig{Type: transport, ExternalHTTP: &v2config.ExternalHTTPTransport{Auth: v2config.HTTPAuthConfig{Mode: v2config.HTTPAuthNone}}}
	default:
		return v2config.ServerEntry{}, errors.New("unsupported v2 transport type")
	}
	return entry, nil
}

func httpAuth(entry *v2config.ServerEntry) (*v2config.HTTPAuthConfig, error) {
	if entry == nil {
		return nil, errors.New("server entry is required")
	}
	switch entry.Transport.Type {
	case v2config.TransportManagedHTTP:
		if entry.Transport.ManagedHTTP == nil {
			return nil, errors.New("managed HTTP configuration is missing")
		}
		return &entry.Transport.ManagedHTTP.Auth, nil
	case v2config.TransportExternalHTTP:
		if entry.Transport.ExternalHTTP == nil {
			return nil, errors.New("external HTTP configuration is missing")
		}
		return &entry.Transport.ExternalHTTP.Auth, nil
	default:
		return nil, errors.New("HTTP authentication is only valid for HTTP transports")
	}
}

func ConfigureNoHTTPAuth(entry *v2config.ServerEntry) error {
	auth, err := httpAuth(entry)
	if err != nil {
		return err
	}
	*auth = v2config.HTTPAuthConfig{Mode: v2config.HTTPAuthNone}
	return nil
}

func ConfigureOAuthAuth(entry *v2config.ServerEntry, scopes []string) error {
	auth, err := httpAuth(entry)
	if err != nil {
		return err
	}
	clean := make([]string, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		clean = append(clean, scope)
	}
	*auth = v2config.HTTPAuthConfig{Mode: v2config.HTTPAuthOAuth, OAuth: &v2config.OAuthAuthConfig{Scopes: clean}}
	return nil
}

// ConfigureStaticAuth stores the credential in the native secret store and
// writes only the deterministic internal reference into the Server Entry.
func (a *V2App) ConfigureStaticAuth(ctx context.Context, entry *v2config.ServerEntry, headerName, scheme string, credential []byte) error {
	if entry == nil || strings.TrimSpace(entry.ID) == "" {
		return errors.New("server entry with an id is required")
	}
	auth, err := httpAuth(entry)
	if err != nil {
		return err
	}
	headerName = http.CanonicalHeaderKey(strings.TrimSpace(headerName))
	if headerName == "" {
		headerName = "Authorization"
	}
	scheme = strings.TrimSpace(scheme)
	ref := StaticAuthSecretRef(entry.ID)
	if len(credential) != 0 {
		if _, err := a.PutStaticAuthSecret(ctx, entry.ID, credential); err != nil {
			return err
		}
	} else if _, err := a.secrets.Get(ctx, ref); err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return errors.New("static HTTP credential is required")
		}
		return err
	}
	*auth = v2config.HTTPAuthConfig{Mode: v2config.HTTPAuthStatic, Static: &v2config.StaticAuthConfig{
		HeaderName: headerName,
		Scheme: scheme,
		SecretRef: ref,
	}}
	return nil
}

func (a *V2App) StaticAuthCredentialConfigured(ctx context.Context, serverID string) bool {
	_, err := a.secrets.Get(ctx, StaticAuthSecretRef(serverID))
	return err == nil
}

// ConfigureEnvironmentSecret stores one environment value as a secret while
// exposing only its environment-variable name to native callers.
func (a *V2App) ConfigureEnvironmentSecret(ctx context.Context, entry *v2config.ServerEntry, name string, value []byte) error {
	if entry == nil || strings.TrimSpace(entry.ID) == "" {
		return errors.New("server entry with an id is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("environment variable name is required")
	}
	ref := EnvironmentSecretRef(entry.ID, name)
	if len(value) != 0 {
		if _, err := a.PutEnvironmentSecret(ctx, entry.ID, name, value); err != nil {
			return err
		}
	} else if _, err := a.secrets.Get(ctx, ref); err != nil {
		if errors.Is(err, secrets.ErrNotFound) {
			return errors.New("environment secret value is required")
		}
		return err
	}
	if entry.Environment.SecretRefs == nil {
		entry.Environment.SecretRefs = make(map[string]string)
	}
	entry.Environment.SecretRefs[name] = ref
	return nil
}

// EnvironmentSecretNames projects only the user-facing variable names. Secret
// references and values remain internal.
func EnvironmentSecretNames(entry v2config.ServerEntry) []string {
	names := make([]string, 0, len(entry.Environment.SecretRefs))
	for name := range entry.Environment.SecretRefs {
		names = append(names, name)
	}
	return names
}
