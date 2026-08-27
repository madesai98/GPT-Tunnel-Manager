package servers

import (
	"errors"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/lifecycle"
)

type Source string

const (
	SourceUI    Source = "ui"
	SourceMCP   Source = "mcp"
	SourceApp   Source = "app"
	SourceRetry Source = "retry"
	SourceIdle  Source = "idle"
)

var (
	ErrDisabled     = errors.New("server_disabled")
	ErrModeConflict = errors.New("mode_not_mcp_controllable")
	ErrAlwaysOnStop = errors.New("always_on_cannot_stop")
	ErrNotFound     = errors.New("server_not_found")
)

type Snapshot struct {
	ServerID            string                  `json:"server_id"`
	Name                string                  `json:"name"`
	Enabled             bool                    `json:"enabled"`
	Mode                string                  `json:"mode"`
	Desired             lifecycle.DesiredState  `json:"desired_state"`
	Observed            lifecycle.ObservedState `json:"observed_state"`
	Phase               lifecycle.Phase         `json:"phase,omitempty"`
	Ready               bool                    `json:"ready"`
	TunnelReady         bool                    `json:"tunnel_ready"`
	IdleShutdownEnabled bool                    `json:"idle_shutdown_enabled"`
	ActivityTracking    string                  `json:"activity_tracking"`
	LastActivityAt      *time.Time              `json:"last_activity_at,omitempty"`
	RetryAfter          *time.Time              `json:"retry_after,omitempty"`
	LastError           *StatusError            `json:"last_error,omitempty"`
}

type StatusError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
