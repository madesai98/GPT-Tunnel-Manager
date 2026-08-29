package discovery

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

const toolReferencePrefix = "tr1."

type toolReferenceClaims struct {
	Version           int    `json:"version"`
	GenerationID      string `json:"generation_id"`
	ServerID          string `json:"server_id"`
	ToolName          string `json:"tool_name"`
	SourceFingerprint string `json:"source_fingerprint"`
}

func encodeToolReference(claims toolReferenceClaims) (string, error) {
	claims.Version = 1
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return toolReferencePrefix + base64.RawURLEncoding.EncodeToString(body), nil
}

func decodeToolReference(value string) (toolReferenceClaims, error) {
	if !strings.HasPrefix(value, toolReferencePrefix) {
		return toolReferenceClaims{}, ErrInvalidToolReference
	}
	body, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, toolReferencePrefix))
	if err != nil {
		return toolReferenceClaims{}, ErrInvalidToolReference
	}
	var claims toolReferenceClaims
	if err := json.Unmarshal(body, &claims); err != nil || claims.Version != 1 ||
		strings.TrimSpace(claims.GenerationID) == "" || strings.TrimSpace(claims.ServerID) == "" ||
		strings.TrimSpace(claims.ToolName) == "" || strings.TrimSpace(claims.SourceFingerprint) == "" {
		return toolReferenceClaims{}, ErrInvalidToolReference
	}
	return claims, nil
}
