package executionhandle

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const tokenPrefix = "eh1"

var (
	ErrInvalidHandle = errors.New("invalid_execution_handle")
	ErrStaleHandle   = errors.New("stale_execution_handle")
)

type Claims struct {
	Version           int    `json:"version"`
	GenerationID      string `json:"generation_id"`
	ServerID          string `json:"server_id"`
	ToolName          string `json:"tool_name"`
	SourceFingerprint string `json:"source_fingerprint"`
	ExecutorClass     string `json:"executor_class"`
	ProcessEpoch      string `json:"process_epoch"`
}

type Manager struct {
	key   [32]byte
	epoch string
}

func NewManager() (*Manager, error) {
	manager := &Manager{}
	if _, err := rand.Read(manager.key[:]); err != nil {
		return nil, fmt.Errorf("generate execution-handle key: %w", err)
	}
	var epoch [16]byte
	if _, err := rand.Read(epoch[:]); err != nil {
		return nil, fmt.Errorf("generate execution-handle process epoch: %w", err)
	}
	manager.epoch = base64.RawURLEncoding.EncodeToString(epoch[:])
	return manager, nil
}

func (m *Manager) ProcessEpoch() string {
	if m == nil {
		return ""
	}
	return m.epoch
}

func (m *Manager) Mint(claims Claims) (string, error) {
	if m == nil || m.epoch == "" {
		return "", errors.New("execution-handle manager is not initialized")
	}
	if err := validateClaims(claims, false); err != nil {
		return "", err
	}
	claims.Version = 1
	claims.ProcessEpoch = m.epoch
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal execution handle: %w", err)
	}
	mac := hmac.New(sha256.New, m.key[:])
	_, _ = mac.Write(payload)
	signature := mac.Sum(nil)
	return tokenPrefix + "." + base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (m *Manager) Validate(token string) (Claims, error) {
	if m == nil || m.epoch == "" {
		return Claims{}, ErrInvalidHandle
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != tokenPrefix {
		return Claims{}, ErrInvalidHandle
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrInvalidHandle
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, ErrInvalidHandle
	}
	mac := hmac.New(sha256.New, m.key[:])
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return Claims{}, ErrInvalidHandle
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Claims{}, ErrInvalidHandle
	}
	if claims.Version != 1 {
		return Claims{}, ErrInvalidHandle
	}
	if err := validateClaims(claims, true); err != nil {
		return Claims{}, ErrInvalidHandle
	}
	if !hmac.Equal([]byte(claims.ProcessEpoch), []byte(m.epoch)) {
		return Claims{}, ErrStaleHandle
	}
	return claims, nil
}

func validateClaims(claims Claims, requireEpoch bool) error {
	if strings.TrimSpace(claims.GenerationID) == "" || strings.TrimSpace(claims.ServerID) == "" ||
		strings.TrimSpace(claims.ToolName) == "" || strings.TrimSpace(claims.SourceFingerprint) == "" ||
		strings.TrimSpace(claims.ExecutorClass) == "" {
		return errors.New("execution handle claims are incomplete")
	}
	if requireEpoch && strings.TrimSpace(claims.ProcessEpoch) == "" {
		return errors.New("execution handle process epoch is missing")
	}
	return nil
}
