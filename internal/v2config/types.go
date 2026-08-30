package v2config

import "time"

const (
	SchemaVersion                   = 2
	ManagerRuntimeCredentialRef     = "secret://openai/runtime/default"
	LocalManagerCapabilitySecretRef = "secret://manager/local-capability/default"
	DefaultEmbeddingModelID         = "bge-small-en-v1.5-q8_0"
	DefaultLlamaCppRelease          = "b10621"
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

const (
	EmbeddingProviderLocalGGUF        EmbeddingProvider = "local_gguf"
	EmbeddingProviderOpenAICompatible EmbeddingProvider = "openai_compatible" // legacy v2 config migration only
)

type EmbeddingRuntimeConfig struct {
	Release    string `json:"release"`
	BinaryPath string `json:"binary_path,omitempty"`
}

type EmbeddingModel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
	FileName    string `json:"file_name"`
	SHA256      string `json:"sha256,omitempty"`
	Dimensions  int    `json:"dimensions"`
	Pooling     string `json:"pooling"`
}

type EmbeddingConfig struct {
	Provider EmbeddingProvider      `json:"provider"`
	Model    string                 `json:"model"`
	Runtime  EmbeddingRuntimeConfig `json:"runtime"`
	Models   []EmbeddingModel       `json:"models"`

	// Legacy fields are accepted only so existing v2 installations can be
	// upgraded in place from the removed OpenAI-compatible embedding backend.
	BaseURL       string `json:"base_url,omitempty"`
	Dimensions    *int   `json:"dimensions,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty"`
}

func DefaultEmbeddingModels() []EmbeddingModel {
	return []EmbeddingModel{{
		ID:          DefaultEmbeddingModelID,
		Name:        "BGE Small EN v1.5 Q8_0",
		DownloadURL: "https://huggingface.co/ggml-org/bge-small-en-v1.5-Q8_0-GGUF/resolve/main/bge-small-en-v1.5-q8_0.gguf?download=true",
		FileName:    "bge-small-en-v1.5-q8_0.gguf",
		SHA256:      "f046db1dc724cf4f6f0a0c5917e922823b73eb1d27b8f9a9c2797f7866974804",
		Dimensions:  384,
		Pooling:     "cls",
	}}
}

func DefaultEmbeddingConfig() EmbeddingConfig {
	return EmbeddingConfig{
		Provider: EmbeddingProviderLocalGGUF,
		Model:    DefaultEmbeddingModelID,
		Runtime:  EmbeddingRuntimeConfig{Release: DefaultLlamaCppRelease},
		Models:   DefaultEmbeddingModels(),
	}
}

// EffectiveEmbeddingConfig maps the released v2 OpenAI-compatible setting to
// the local default without ever constructing an online embedding client. The
// next manager save persists the local configuration.
func EffectiveEmbeddingConfig(c EmbeddingConfig) EmbeddingConfig {
	if c.Provider == EmbeddingProviderOpenAICompatible || c.Provider == "" {
		return DefaultEmbeddingConfig()
	}
	return c
}

func (c EmbeddingConfig) SelectedModel() (EmbeddingModel, bool) {
	for _, model := range c.Models {
		if model.ID == c.Model {
			return model, true
		}
	}
	return EmbeddingModel{}, false
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
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	Mode           ServerMode           `json:"mode"`
	Transport      TransportConfig      `json:"transport"`
	Environment    EnvironmentConfig    `json:"environment"`
	Runtime        RuntimeConfig        `json:"runtime"`
	Logging        ServerLoggingConfig  `json:"logging"`
	ToolVisibility ToolVisibilityConfig `json:"tool_visibility,omitempty"`
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

type ToolVisibilityConfig struct {
	Hidden []string `json:"hidden,omitempty"`
}

func (e ServerEntry) ToolExposed(name string) bool {
	for _, hidden := range e.ToolVisibility.Hidden {
		if hidden == name {
			return false
		}
	}
	return true
}

func DefaultManagerConfig(port int) ManagerConfig {
	return ManagerConfig{
		SchemaVersion: SchemaVersion,
		LocalManager: LocalManagerConfig{
			Port:                    port,
			AccessProtectionEnabled: true,
		},
		ManagerTunnel: ManagerTunnelConfig{RuntimeCredentialRef: ManagerRuntimeCredentialRef},
		Embedding:     DefaultEmbeddingConfig(),
		Index:         IndexConfig{QueryEmbeddingCacheEntries: 256},
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
