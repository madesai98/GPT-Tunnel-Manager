package v2config

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var (
	serverIDPattern = regexp.MustCompile(`^srv_[a-f0-9]{32}$`)
	tunnelIDPattern = regexp.MustCompile(`^tunnel_[a-f0-9]{32}$`)
	envNamePattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

var restrictedStaticHeaders = map[string]struct{}{
	"Connection":        {},
	"Content-Length":    {},
	"Host":              {},
	"Proxy-Connection":  {},
	"Te":                {},
	"Trailer":           {},
	"Transfer-Encoding": {},
	"Upgrade":           {},
}

func ValidateManager(c ManagerConfig) error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported manager schema_version %d", c.SchemaVersion)
	}
	if c.LocalManager.Port < 1024 || c.LocalManager.Port > 65535 {
		return errors.New("local_manager.port must be between 1024 and 65535")
	}
	if c.ManagerTunnel.TunnelID != "" && !tunnelIDPattern.MatchString(c.ManagerTunnel.TunnelID) {
		return errors.New("manager_tunnel.tunnel_id must match tunnel_<32 lowercase hexadecimal chars>")
	}
	if c.ManagerTunnel.Enabled && c.ManagerTunnel.TunnelID == "" {
		return errors.New("manager_tunnel.tunnel_id is required when manager tunnel is enabled")
	}
	if c.ManagerTunnel.RuntimeCredentialRef != "" {
		if err := validateSecretRef(c.ManagerTunnel.RuntimeCredentialRef); err != nil {
			return fmt.Errorf("manager_tunnel.runtime_credential_ref: %w", err)
		}
	}
	if err := validateEmbedding(c.Embedding); err != nil {
		return err
	}
	if strings.TrimSpace(c.Routing.DefaultProfile) != c.Routing.DefaultProfile {
		return errors.New("routing.default_profile may not have leading or trailing whitespace")
	}
	if len(c.Routing.DefaultProfile) > 128 {
		return errors.New("routing.default_profile may not exceed 128 bytes")
	}
	if c.Index.QueryEmbeddingCacheEntries < 0 || c.Index.QueryEmbeddingCacheEntries > 4096 {
		return errors.New("index.query_embedding_cache_entries must be between 0 and 4096")
	}
	if c.General.CloseBehavior != "minimize" && c.General.CloseBehavior != "exit" {
		return errors.New("general.close_behavior must be minimize or exit")
	}
	if c.ManagedDefaults.IdleTimeoutSeconds < 0 {
		return errors.New("managed_defaults.idle_timeout_seconds cannot be negative")
	}
	if !validLogLevel(c.Logging.CaptureLevel) {
		return errors.New("logging.capture_level must be trace, debug, info, warn, or error")
	}
	if !validDisplayLevel(c.Logging.DisplayLevel) {
		return errors.New("logging.display_level must be all, trace, debug, info, warn, or error")
	}
	if !validLogLevel(c.Logging.DiskMinimumLevel) {
		return errors.New("logging.disk_minimum_level must be trace, debug, info, warn, or error")
	}
	switch c.Logging.MemoryLimitMB {
	case 5, 10, 25, 50, 100:
	default:
		return errors.New("logging.memory_limit_mb must be one of 5, 10, 25, 50, 100")
	}
	if c.Logging.MaximumFileSizeMB <= 0 || c.Logging.KeepFiles <= 0 {
		return errors.New("logging disk rotation limits must be positive")
	}
	if c.TunnelClient.UpdateCheckIntervalHours <= 0 {
		return errors.New("tunnel_client.update_check_interval_hours must be positive")
	}
	switch strings.ToLower(strings.TrimSpace(c.TunnelClient.Channel)) {
	case "stable", "prerelease":
	default:
		return errors.New("tunnel_client.channel must be stable or prerelease")
	}
	switch c.Appearance.Theme {
	case "system", "light", "dark":
	default:
		return errors.New("appearance.theme must be system, light, or dark")
	}
	return nil
}

func validateEmbedding(c EmbeddingConfig) error {
	if c.Provider != EmbeddingProviderOpenAICompatible {
		return fmt.Errorf("embedding.provider must be %q", EmbeddingProviderOpenAICompatible)
	}
	if strings.TrimSpace(c.Model) == "" {
		return errors.New("embedding.model is required")
	}
	u, err := validateHTTPURL(c.BaseURL)
	if err != nil {
		return fmt.Errorf("embedding.base_url: %w", err)
	}
	if err := validateSecretRef(c.CredentialRef); err != nil {
		return fmt.Errorf("embedding.credential_ref: %w", err)
	}
	if u.Scheme != "https" && !isLoopbackHost(u.Hostname()) {
		return errors.New("embedding.base_url must use https unless it is loopback")
	}
	if c.Dimensions != nil && (*c.Dimensions <= 0 || *c.Dimensions > 65536) {
		return errors.New("embedding.dimensions must be between 1 and 65536")
	}
	return nil
}

func validLogLevel(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "trace", "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}

func validDisplayLevel(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "all") || validLogLevel(value)
}

func ValidateServers(c ServersConfig) error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported servers schema_version %d", c.SchemaVersion)
	}
	seen := make(map[string]struct{}, len(c.Servers))
	for i, e := range c.Servers {
		if err := ValidateServer(e); err != nil {
			return fmt.Errorf("servers[%d]: %w", i, err)
		}
		if _, ok := seen[e.ID]; ok {
			return fmt.Errorf("servers[%d]: duplicate server id %s", i, e.ID)
		}
		seen[e.ID] = struct{}{}
	}
	return nil
}

func ValidateServer(e ServerEntry) error {
	if !serverIDPattern.MatchString(e.ID) {
		return errors.New("id must match srv_<32 lowercase hex chars>")
	}
	if strings.TrimSpace(e.Name) == "" {
		return errors.New("name is required")
	}
	switch e.Mode {
	case ModeAlwaysOn, ModeManaged, ModeManual, ModeDisabled:
	default:
		return fmt.Errorf("invalid mode %q", e.Mode)
	}
	if e.Runtime.StartupTimeoutSeconds < 0 || e.Runtime.ShutdownTimeoutSeconds < 0 {
		return errors.New("runtime timeouts cannot be negative")
	}
	if e.Runtime.IdleTimeoutSeconds != nil && *e.Runtime.IdleTimeoutSeconds < 0 {
		return errors.New("runtime.idle_timeout_seconds cannot be negative")
	}
	if e.Logging.CaptureLevelOverride != nil && !validLogLevel(*e.Logging.CaptureLevelOverride) {
		return errors.New("logging.capture_level_override must be trace, debug, info, warn, or error")
	}
	for k := range e.Environment.Values {
		if !envNamePattern.MatchString(k) {
			return fmt.Errorf("invalid environment name %q", k)
		}
	}
	for k, v := range e.Environment.SecretRefs {
		if !envNamePattern.MatchString(k) {
			return fmt.Errorf("invalid environment secret name %q", k)
		}
		if err := validateSecretRef(v); err != nil {
			return fmt.Errorf("environment secret %q: %w", k, err)
		}
	}
	return validateTransport(e.Transport)
}

func validateTransport(t TransportConfig) error {
	switch t.Type {
	case TransportStdio:
		if t.Stdio == nil || t.ManagedHTTP != nil || t.ExternalHTTP != nil {
			return errors.New("stdio transport must contain only stdio configuration")
		}
		if strings.TrimSpace(t.Stdio.Executable) == "" {
			return errors.New("stdio.executable is required")
		}
	case TransportManagedHTTP:
		if t.ManagedHTTP == nil || t.Stdio != nil || t.ExternalHTTP != nil {
			return errors.New("managed_http transport must contain only managed_http configuration")
		}
		if strings.TrimSpace(t.ManagedHTTP.Launch.Executable) == "" {
			return errors.New("managed_http.launch.executable is required")
		}
		if err := validateHTTPEndpoint(t.ManagedHTTP.URL, t.ManagedHTTP.Auth, t.ManagedHTTP.AllowInsecureCredentialTransport); err != nil {
			return fmt.Errorf("managed_http: %w", err)
		}
	case TransportExternalHTTP:
		if t.ExternalHTTP == nil || t.Stdio != nil || t.ManagedHTTP != nil {
			return errors.New("external_http transport must contain only external_http configuration")
		}
		if err := validateHTTPEndpoint(t.ExternalHTTP.URL, t.ExternalHTTP.Auth, t.ExternalHTTP.AllowInsecureCredentialTransport); err != nil {
			return fmt.Errorf("external_http: %w", err)
		}
	default:
		return fmt.Errorf("invalid transport type %q", t.Type)
	}
	return nil
}

func validateHTTPEndpoint(rawURL string, auth HTTPAuthConfig, allowInsecure bool) error {
	u, err := validateHTTPURL(rawURL)
	if err != nil {
		return fmt.Errorf("url: %w", err)
	}
	if err := validateHTTPAuth(auth); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if auth.Mode != HTTPAuthNone && u.Scheme != "https" && !isLoopbackHost(u.Hostname()) && !allowInsecure {
		return errors.New("credential-bearing remote HTTP requires https unless allow_insecure_credential_transport is enabled")
	}
	return nil
}

func validateHTTPAuth(auth HTTPAuthConfig) error {
	switch auth.Mode {
	case HTTPAuthNone:
		if auth.OAuth != nil || auth.Static != nil {
			return errors.New("none auth may not contain oauth or static settings")
		}
	case HTTPAuthOAuth:
		if auth.OAuth == nil || auth.Static != nil {
			return errors.New("oauth auth must contain only oauth settings")
		}
		seen := map[string]struct{}{}
		for _, scope := range auth.OAuth.Scopes {
			if strings.TrimSpace(scope) == "" || strings.TrimSpace(scope) != scope {
				return errors.New("oauth scopes must be non-empty and trimmed")
			}
			if _, ok := seen[scope]; ok {
				return fmt.Errorf("duplicate oauth scope %q", scope)
			}
			seen[scope] = struct{}{}
		}
	case HTTPAuthStatic:
		if auth.Static == nil || auth.OAuth != nil {
			return errors.New("static auth must contain only static settings")
		}
		name := http.CanonicalHeaderKey(strings.TrimSpace(auth.Static.HeaderName))
		if name == "" || !validHeaderName(name) {
			return errors.New("static.header_name is invalid")
		}
		if _, restricted := restrictedStaticHeaders[name]; restricted {
			return fmt.Errorf("static.header_name %q is transport-controlled", name)
		}
		if strings.ContainsAny(auth.Static.Scheme, "\r\n") {
			return errors.New("static.scheme may not contain CR or LF")
		}
		if err := validateSecretRef(auth.Static.SecretRef); err != nil {
			return fmt.Errorf("static.secret_ref: %w", err)
		}
	default:
		return fmt.Errorf("invalid auth mode %q", auth.Mode)
	}
	return nil
}

func validateHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("scheme must be http or https")
	}
	if u.Host == "" {
		return nil, errors.New("host is required")
	}
	if u.User != nil {
		return nil, errors.New("userinfo is not allowed")
	}
	return u, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateSecretRef(ref string) error {
	if !strings.HasPrefix(ref, "secret://") || len(ref) == len("secret://") {
		return errors.New("secret reference must use secret://")
	}
	return nil
}

func validHeaderName(name string) bool {
	for _, r := range name {
		if r <= 32 || r >= 127 || strings.ContainsRune("()<>@,;:\\[]?={} \t", r) {
			return false
		}
	}
	return true
}
