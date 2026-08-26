package config

import "time"

const SchemaVersion = 1

type ManagerConfig struct {
	SchemaVersion   int                `json:"schema_version"`
	ManagerTunnel   TunnelConfig       `json:"manager_tunnel"`
	General         GeneralConfig      `json:"general"`
	ManagedDefaults ManagedDefaults    `json:"managed_defaults"`
	Logging         LoggingConfig      `json:"logging"`
	TunnelClient    TunnelClientConfig `json:"tunnel_client"`
	Appearance      AppearanceConfig   `json:"appearance"`
}

type TunnelConfig struct {
	TunnelID             string `json:"tunnel_id"`
	RuntimeCredentialRef string `json:"runtime_credential_ref,omitempty"`
}

type GeneralConfig struct {
	LaunchAtStartup bool   `json:"launch_at_startup"`
	StartMinimized  bool   `json:"start_minimized"`
	MinimizeToTray  bool   `json:"minimize_to_tray"`
	CloseBehavior   string `json:"close_behavior"`
	ConfirmExit     bool   `json:"confirm_exit"`
}

type ManagedDefaults struct { IdleTimeoutSeconds int `json:"idle_timeout_seconds"` }

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

type AppearanceConfig struct { Theme string `json:"theme"` }

type ServersConfig struct {
	SchemaVersion int           `json:"schema_version"`
	Servers       []ServerEntry `json:"servers"`
}

type ServerEntry struct {
	ID                string              `json:"id"`
	Name              string              `json:"name"`
	ChatGPTPluginName string              `json:"chatgpt_plugin_name,omitempty"`
	Enabled           bool                `json:"enabled"`
	Mode              ServerMode          `json:"mode"`
	Transport         TransportConfig     `json:"transport"`
	Tunnel            TunnelConfig        `json:"tunnel"`
	Environment       EnvironmentConfig   `json:"environment"`
	Runtime           RuntimeConfig       `json:"runtime"`
	Logging           ServerLoggingConfig `json:"logging"`
}

type ServerMode string
const (
	ModeAlwaysOn ServerMode = "always_on"
	ModeManaged ServerMode = "managed"
	ModeManual ServerMode = "manual"
)

type TransportType string
const (
	TransportStdio TransportType = "stdio"
	TransportManagedHTTP TransportType = "managed_http"
	TransportExternalHTTP TransportType = "external_http"
)

type TransportConfig struct {
	Type TransportType `json:"type"`
	Stdio *StdioTransport `json:"stdio,omitempty"`
	ManagedHTTP *ManagedHTTPTransport `json:"managed_http,omitempty"`
	ExternalHTTP *ExternalHTTPTransport `json:"external_http,omitempty"`
}

type LaunchConfig struct { Executable string `json:"executable"`; Args []string `json:"args,omitempty"`; WorkingDirectory string `json:"working_directory,omitempty"` }
type StdioTransport struct { Executable string `json:"executable"`; Args []string `json:"args,omitempty"`; WorkingDirectory string `json:"working_directory,omitempty"` }
type ManagedHTTPTransport struct { URL string `json:"url"`; Launch LaunchConfig `json:"launch"` }
type ExternalHTTPTransport struct { URL string `json:"url"` }
type EnvironmentConfig struct { Values map[string]string `json:"values,omitempty"`; SecretRefs map[string]string `json:"secret_refs,omitempty"` }
type RuntimeConfig struct { StartupTimeoutSeconds int `json:"startup_timeout_seconds"`; ShutdownTimeoutSeconds int `json:"shutdown_timeout_seconds"`; IdleTimeoutSeconds *int `json:"idle_timeout_seconds,omitempty"` }
type ServerLoggingConfig struct { CaptureLevelOverride *string `json:"capture_level_override,omitempty"` }

func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{SchemaVersion: SchemaVersion, General: GeneralConfig{MinimizeToTray:true, CloseBehavior:"minimize", ConfirmExit:true}, ManagedDefaults: ManagedDefaults{IdleTimeoutSeconds:300}, Logging: LoggingConfig{CaptureLevel:"info", DisplayLevel:"info", MemoryLimitMB:25, DiskMinimumLevel:"debug", MaximumFileSizeMB:10, KeepFiles:5}, TunnelClient: TunnelClientConfig{AutoUpdate:true, Channel:"stable", UpdateCheckIntervalHours:24}, Appearance: AppearanceConfig{Theme:"system"}}
}
func DefaultServersConfig() ServersConfig { return ServersConfig{SchemaVersion:SchemaVersion, Servers:[]ServerEntry{}} }
func (e ServerEntry) StartupTimeout() time.Duration { n:=e.Runtime.StartupTimeoutSeconds; if n<=0 { n=30 }; return time.Duration(n)*time.Second }
func (e ServerEntry) ShutdownTimeout() time.Duration { n:=e.Runtime.ShutdownTimeoutSeconds; if n<=0 { n=10 }; return time.Duration(n)*time.Second }
func (e ServerEntry) IdleTimeout(def int) time.Duration { n:=def; if e.Runtime.IdleTimeoutSeconds!=nil { n=*e.Runtime.IdleTimeoutSeconds }; if n<=0 { return 0 }; return time.Duration(n)*time.Second }
