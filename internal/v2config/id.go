package v2config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func NewServerID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate server id: %w", err)
	}
	return "srv_" + hex.EncodeToString(b), nil
}
