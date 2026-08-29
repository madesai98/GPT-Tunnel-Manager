package routingprefs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
)

var ErrPreferenceConflict = errors.New("preference_conflict")

type ReviewState string

const (
	ReviewActive      ReviewState = "active"
	ReviewNeedsReview ReviewState = "needs_review"
)

type Specificity string

const (
	SpecificityServer          Specificity = "server"
	SpecificityToolSet         Specificity = "tool_set"
	SpecificityConditionalTool Specificity = "conditional_tool"
)

type Target struct {
	ServerID              string `json:"server_id"`
	ToolName              string `json:"tool_name"`
	AssumptionFingerprint string `json:"assumption_fingerprint"`
}

type RuleSpec struct {
	ProfileID     string      `json:"profile_id,omitempty"`
	Specificity   Specificity `json:"specificity"`
	SubjectKey    string      `json:"subject_key"`
	Condition     string      `json:"condition,omitempty"`
	Preferred     []Target    `json:"preferred"`
	Deprioritized []Target    `json:"deprioritized,omitempty"`
}

type Rule struct {
	ID                    string      `json:"preference_id"`
	Spec                  RuleSpec    `json:"spec"`
	ConflictKey           string      `json:"conflict_key"`
	AssumptionFingerprint string      `json:"assumption_fingerprint"`
	ReviewState           ReviewState `json:"review_state"`
	UpdatedAt             time.Time   `json:"updated_at"`
}

type Profile struct {
	ID          string    `json:"profile_id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type WriteResult struct {
	PreferenceRevision uint64 `json:"preference_revision"`
	Changed            bool   `json:"changed"`
	NeedsReview        bool   `json:"needs_review,omitempty"`
	ID                 string `json:"id,omitempty"`
}

type ConflictError struct {
	Expected uint64
	Current  uint64
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s: expected preference revision %d, current %d", ErrPreferenceConflict, e.Expected, e.Current)
}

func (e *ConflictError) Unwrap() error { return ErrPreferenceConflict }

type Store struct {
	catalog *catalog.Catalog
	db      *sql.DB
}

func NewStore(c *catalog.Catalog) (*Store, error) {
	if c == nil || c.DB() == nil {
		return nil, errors.New("catalog is required")
	}
	return &Store{catalog: c, db: c.DB()}, nil
}

func PreferenceAssumptionFingerprint(sourceFingerprint, executorClass string) (string, error) {
	if strings.TrimSpace(sourceFingerprint) == "" || strings.TrimSpace(executorClass) == "" {
		return "", errors.New("source fingerprint and executor class are required")
	}
	body, err := json.Marshal(struct {
		SourceFingerprint string `json:"source_fingerprint"`
		ExecutorClass     string `json:"executor_class"`
	}{SourceFingerprint: sourceFingerprint, ExecutorClass: executorClass})
	if err != nil {
		return "", err
	}
	return toolcontract.FingerprintJSON(body), nil
}

func TargetMapKey(serverID, toolName string) string { return serverID + "\x00" + toolName }

func CurrentAssumptions(sources []catalog.SourceToolRecord) (map[string]string, error) {
	result := make(map[string]string, len(sources))
	for _, source := range sources {
		executorClass, err := toolcontract.ExecutorClassForJSON(source.ContractJSON)
		if err != nil {
			return nil, fmt.Errorf("classify preference target %s/%s: %w", source.ServerID, source.ToolName, err)
		}
		semanticFingerprint, err := toolcontract.SemanticSourceFingerprintJSON(source.ContractJSON)
		if err != nil {
			return nil, fmt.Errorf("fingerprint preference target %s/%s: %w", source.ServerID, source.ToolName, err)
		}
		assumption, err := PreferenceAssumptionFingerprint(semanticFingerprint, string(executorClass))
		if err != nil {
			return nil, err
		}
		result[TargetMapKey(source.ServerID, source.ToolName)] = assumption
	}
	return result, nil
}

func (s *Store) Revision(ctx context.Context) (uint64, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT preference_revision FROM routing_state WHERE singleton = 1`).Scan(&raw); err != nil {
		return 0, fmt.Errorf("load preference revision: %w", err)
	}
	return parseRevision(raw)
}
