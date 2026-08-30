package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

var (
	ErrInvalidVector       = errors.New("invalid embedding vector")
	ErrDimensionMismatch   = errors.New("embedding dimension mismatch")
	ErrZeroVector          = errors.New("embedding vector has zero magnitude")
	ErrModelNotInstalled   = errors.New("local embedding model is not installed")
	ErrRuntimeNotInstalled = errors.New("local embedding runtime is not installed")
)

const IdentityVersion = "embedding-provider/v2"

type Identity struct {
	Provider    string `json:"provider"`
	BaseURL     string `json:"base_url,omitempty"` // legacy artifact compatibility only
	Model       string `json:"model"`
	ModelSHA256 string `json:"model_sha256,omitempty"`
	Dimensions  *int   `json:"dimensions,omitempty"`
	Pooling     string `json:"pooling,omitempty"`
	Runtime     string `json:"runtime,omitempty"`
	Protocol    string `json:"protocol"`
}

func (i Identity) Fingerprint() string {
	body, _ := json.Marshal(i)
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func (i Identity) Validate() error {
	if strings.TrimSpace(i.Provider) == "" {
		return errors.New("embedding provider identity is missing provider")
	}
	if strings.TrimSpace(i.Model) == "" {
		return errors.New("embedding provider identity is missing model")
	}
	if i.Protocol == "" {
		return errors.New("embedding provider identity is missing protocol")
	}
	if i.Dimensions != nil && *i.Dimensions <= 0 {
		return errors.New("embedding provider identity dimensions must be positive")
	}
	return nil
}

type Provider interface {
	Identity() Identity
	Embed(context.Context, []string) ([][]float32, error)
}

type Closer interface {
	Close() error
}

func ValidateVector(vector []float32, expectedDimensions int) error {
	if len(vector) == 0 {
		return fmt.Errorf("%w: vector is empty", ErrInvalidVector)
	}
	if expectedDimensions > 0 && len(vector) != expectedDimensions {
		return fmt.Errorf("%w: got %d, want %d", ErrDimensionMismatch, len(vector), expectedDimensions)
	}
	var magnitude float64
	for index, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return fmt.Errorf("%w: component %d is not finite", ErrInvalidVector, index)
		}
		magnitude += float64(value) * float64(value)
	}
	if magnitude == 0 {
		return ErrZeroVector
	}
	return nil
}
