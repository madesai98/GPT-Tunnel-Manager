package v2config

import "time"

const (
	SchemaVersion                   = 2
	ManagerRuntimeCredentialRef     = "secret://openai/runtime/default"
	EmbeddingCredentialRef          = "secret://embedding/openai-compatible/default"
	LocalManagerCapabilitySecretRef = "secret://manager/local-capability/default"
)

type ManagerConfig struct {
	SchemaVersion   int                 `json:"schema_version"`
	LocalManager    LocalManagerConfig  `json:"local_manager"`
	ManagerTunnel   ManagerTunnelConfig `json:"manager_tunnel"`
	Embedding       EmbeddingConfig     `json:"embedding"`
	Routing         RoutingConfig       `json:"routing"`
	Index           IndexConfig         `json:"index"`
	General         GeneralConfig       `json:"general"`
	ManagedDefaults ManagedDefaults     `json:"managed_defaults"`
	Logging         LoggingConfig       `json:"logging"`
	TunnelClient    TunnelClientConfig  `json:"tunnel_client"`
	Appearance      AppearanceConfig    `json:"appearance"`
}

type LocalManagerConfig struct {
	Port                    int  `json:"port"`
	AccessProtectionEnabled bool `json:"access_protection_enabled"`
}

type ManagerTunnelConfig struct {
	Enabled              bool   `json:"enabled"`
	TunnelID             string `json:"tunnel_id,omitempty"`
	RuntimeCredentialRef string `json:"runtime_credential_ref,omitempty"`
}

type EmbeddingProvider string

const EmbeddingProviderOpenAICompatible EmbeddingProvider = "openai_compatible"

type EmbeddingConfig struct {
	Provider      EmbeddingProvider `json:"provider"`
	BaseURL       string            `json:"base_url"`
	Model         string            `json:"model"`
	Dimensions    *int              `json:"dimensions,omitempty"`
	CredentialRef string            `json:"credential_ref"`
}

type RoutingConfig struct {
	DefaultProfile string `json:"default_profile,omitempty"`
}

type IndexConfig struct {
	QueryEmbeddingCacheEntries int `json:"query_embedding_cache_entries"`
}

type GeneralConfig struct {
	LaunchAtStartup bool   `json:"launch_at_startup"`
	StartMinimized  bool   `json:"start_minimized"`
	MinimizeToTray  bool   `json:"minimize_to_tray"`
	CloseBehavior   string `json:"close_behavior"`
	ConfirmExit     bool   `json:"confirm_exit"`
}

type ManagedDefaults struct {
	IdleTimeoutSeconds int `json:"idle_timeout_seconds"`
}

type LoggingConfig struct {
	CaptureLevel      string `json:"capture_level"`
	DisplayLevel      string `json:"display_level"`
	MemoryLimitMB     int    `json:"memory_limit_mb"`
	WriteToDisk       bool   `json:"write_to_disk"`
	DiskMinimumLevel  string `json:"disk_minimum_level"`
	MaximumFileSizeMB int    `json:"maximum_file_size_mb"`
	KeepFiles         int    `json:"keep_files"`
}

type TunnelClientConfig struct {
	BinaryPath               string `json:"binary_path,omitempty"`
	AutoUpdate               bool   `json:"auto_update"`
	Channel                  string `json:"channel"`
	UpdateCheckIntervalHours int    `json:"update_check_interval_hours"`
}

type AppearanceConfig struct {
	Theme string `json:"theme"`
}

type ServersConfig struct {
	SchemaVersion int           `json:"schema_version"`
	Servers       []ServerEntry `json:"servers"`
}

type ServerEntry struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Mode        ServerMode          `json:"mode"`
	Transport   TransportConfig     `json:"transport"`
	Environment EnvironmentConfig   `json:"environment"`
	Runtime     RuntimeConfig       `json:"runtime"`
	Logging     ServerLoggingConfig `json:"logging"`
}

type ServerMode string

const (
	ModeAlwaysOn ServerMode = "always_on"
	ModeManaged  ServerMode = "managed"
	ModeManual   ServerMode = "manual"
	ModeDisabled ServerMode = "disabled"
)

type TransportType string

const (
	TransportStdio        TransportType = "stdio"
	TransportManagedHTTP  TransportType = "managed_http"
	TransportExternalHTTP TransportType = "external_http"
)

type TransportConfig struct {
	Type         TransportType          `json:"type"`
	Stdio        *StdioTransport        `json:"stdio,omitempty"`
	ManagedHTTP  *ManagedHTTPTransport  `json:"managed_http,omitempty"`
	ExternalHTTP *ExternalHTTPTransport `json:"external_http,omitempty"`
}

type LaunchConfig struct {
	Executable       string   `json:"executable"`
	Args             []string `json:"args,omitempty"`
	WorkingDirectory string   `json:"working_directory,omitempty"`
}

type StdioTransport struct {
	Executable       string   `json:"executable"`
	Args             []string `json:"args,omitempty"`
	WorkingDirectory string   `json:"working_directory,omitempty"`
}

type ManagedHTTPTransport struct {
	URL                              string         `json:"url"`
	Launch                           LaunchConfig   `json:"launch"`
	Auth                             HTTPAuthConfig `json:"auth"`
	AllowInsecureCredentialTransport bool           `json:"allow_insecure_credential_transport"`
}

type ExternalHTTPTransport struct {
	URL                              string         `json:"url"`
	Auth                             HTTPAuthConfig `json:"auth"`
	AllowInsecureCredentialTransport bool           `json:"allow_insecure_credential_transport"`
}

type HTTPAuthMode string

const (
	HTTPAuthNone   HTTPAuthMode = "none"
	HTTPAuthOAuth  HTTPAuthMode = "oauth"
	HTTPAuthStatic HTTPAuthMode = "static"
)

type HTTPAuthConfig struct {
	Mode   HTTPAuthMode      `json:"mode"`
	OAuth  *OAuthAuthConfig  `json:"oauth,omitempty"`
	Static *StaticAuthConfig `json:"static,omitempty"`
}

type OAuthAuthConfig struct {
	Scopes []string `json:"scopes,omitempty"`
}

type StaticAuthConfig struct {
	HeaderName string `json:"header_name"`
	Scheme     string `json:"scheme,omitempty"`
	SecretRef  string `json:"secret_ref"`
}

type EnvironmentConfig struct {
	Values     map[string]string `json:"values,omitempty"`
	SecretRefs map[string]string `json:"secret_refs,omitempty"`
}

type RuntimeConfig struct {
	StartupTimeoutSeconds  int  `json:"startup_timeout_seconds"`
	ShutdownTimeoutSeconds int  `json:"shutdown_timeout_seconds"`
	IdleTimeoutSeconds     *int `json:"idle_timeout_seconds,omitempty"`
}

type ServerLoggingConfig struct {
	CaptureLevelOverride *string `json:"capture_level_override,omitempty"`
}

func DefaultManagerConfig(port int) ManagerConfig {
	return ManagerConfig{
		SchemaVersion: SchemaVersion,
		LocalManager: LocalManagerConfig{
			Port:                    port,
			AccessProtectionEnabled: true,
		},
		ManagerTunnel: ManagerTunnelConfig{RuntimeCredentialRef: ManagerRuntimeCredentialRef},
		Embedding: EmbeddingConfig{
			Provider:      EmbeddingProviderOpenAICompatible,
			BaseURL:       "https://api.openai.com/v1",
			Model:         "text-embedding-3-small",
			CredentialRef: EmbeddingCredentialRef,
		},
		Index: IndexConfig{QueryEmbeddingCacheEntries: 256},
		General: GeneralConfig{
			MinimizeToTray: true,
			CloseBehavior:  "minimize",
			ConfirmExit:    true,
		},
		ManagedDefaults: ManagedDefaults{IdleTimeoutSeconds: 300},
		Logging: LoggingConfig{
			CaptureLevel:      "trace",
			DisplayLevel:      "info",
			MemoryLimitMB:     25,
			DiskMinimumLevel:  "debug",
			MaximumFileSizeMB: 10,
			KeepFiles:         5,
		},
		TunnelClient: TunnelClientConfig{
			AutoUpdate:               true,
			Channel:                  "stable",
			UpdateCheckIntervalHours: 24,
		},
		Appearance: AppearanceConfig{Theme: "system"},
	}
}

func DefaultServersConfig() ServersConfig {
	return ServersConfig{SchemaVersion: SchemaVersion, Servers: []ServerEntry{}}
}

func (e ServerEntry) StartupTimeout() time.Duration {
	n := e.Runtime.StartupTimeoutSeconds
	if n <= 0 {
		n = 30
	}
	return time.Duration(n) * time.Second
}

func (e ServerEntry) ShutdownTimeout() time.Duration {
	n := e.Runtime.ShutdownTimeoutSeconds
	if n <= 0 {
		n = 10
	}
	return time.Duration(n) * time.Second
}

func (e ServerEntry) IdleTimeout(def int) time.Duration {
	n := def
	if e.Runtime.IdleTimeoutSeconds != nil {
		n = *e.Runtime.IdleTimeoutSeconds
	}
	if n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}
