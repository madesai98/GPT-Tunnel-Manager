package routingprefs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
)

func (s *Store) ListRules(ctx context.Context) ([]Rule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT preference_id, target_key, assumption_fingerprint, review_state, payload_json, updated_at_unix_ms
		FROM routing_preferences ORDER BY preference_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []Rule
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func (s *Store) EffectiveRules(ctx context.Context, profileID string) ([]Rule, error) {
	rules, err := s.ListRules(ctx)
	if err != nil {
		return nil, err
	}
	filtered := rules[:0]
	for _, rule := range rules {
		if rule.ReviewState != ReviewActive {
			continue
		}
		if rule.Spec.ProfileID == "" || (profileID != "" && rule.Spec.ProfileID == profileID) {
			filtered = append(filtered, rule)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		leftProfile := filtered[i].Spec.ProfileID != ""
		rightProfile := filtered[j].Spec.ProfileID != ""
		if leftProfile != rightProfile {
			return leftProfile
		}
		leftPriority := specificityPriority(filtered[i].Spec.Specificity)
		rightPriority := specificityPriority(filtered[j].Spec.Specificity)
		if leftPriority != rightPriority {
			return leftPriority > rightPriority
		}
		return filtered[i].ID < filtered[j].ID
	})
	return filtered, nil
}

func (s *Store) ReconcileTargets(ctx context.Context, currentAssumptions map[string]string) ([]string, uint64, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	currentRevision, err := preferenceRevisionTx(ctx, tx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT preference_id, target_key, assumption_fingerprint, review_state, payload_json, updated_at_unix_ms
		FROM routing_preferences WHERE review_state = ? ORDER BY preference_id
	`, string(ReviewActive))
	if err != nil {
		return nil, 0, err
	}
	var stale []string
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		if ruleTargetsChanged(rule.Spec, currentAssumptions) {
			stale = append(stale, rule.ID)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	if len(stale) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, 0, err
		}
		return nil, currentRevision, nil
	}
	now := time.Now().UTC().UnixMilli()
	for _, id := range stale {
		if _, err := tx.ExecContext(ctx, `UPDATE routing_preferences SET review_state = ?, updated_at_unix_ms = ? WHERE preference_id = ?`, string(ReviewNeedsReview), now, id); err != nil {
			return nil, 0, err
		}
	}
	currentRevision++
	if err := setPreferenceRevisionTx(ctx, tx, currentRevision); err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return stale, currentRevision, nil
}

func normalizeRule(spec RuleSpec) (RuleSpec, string, string, string, []byte, error) {
	spec.ProfileID = strings.TrimSpace(spec.ProfileID)
	spec.SubjectKey = strings.TrimSpace(spec.SubjectKey)
	spec.Condition = strings.TrimSpace(spec.Condition)
	if spec.SubjectKey == "" {
		return RuleSpec{}, "", "", "", nil, errors.New("preference subject key is required")
	}
	switch spec.Specificity {
	case SpecificityServer, SpecificityToolSet:
		if spec.Condition != "" {
			return RuleSpec{}, "", "", "", nil, errors.New("condition is only valid for conditional tool preferences")
		}
	case SpecificityConditionalTool:
		if spec.Condition == "" {
			return RuleSpec{}, "", "", "", nil, errors.New("conditional tool preference requires a condition")
		}
	default:
		return RuleSpec{}, "", "", "", nil, fmt.Errorf("unsupported preference specificity %q", spec.Specificity)
	}
	preferred, err := normalizeTargets(spec.Preferred)
	if err != nil {
		return RuleSpec{}, "", "", "", nil, err
	}
	if len(preferred) == 0 {
		return RuleSpec{}, "", "", "", nil, errors.New("preference requires at least one preferred target")
	}
	deprioritized, err := normalizeTargets(spec.Deprioritized)
	if err != nil {
		return RuleSpec{}, "", "", "", nil, err
	}
	preferredKeys := make(map[string]struct{}, len(preferred))
	for _, target := range preferred {
		preferredKeys[TargetMapKey(target.ServerID, target.ToolName)] = struct{}{}
	}
	for _, target := range deprioritized {
		if _, exists := preferredKeys[TargetMapKey(target.ServerID, target.ToolName)]; exists {
			return RuleSpec{}, "", "", "", nil, fmt.Errorf("target %s/%s cannot be both preferred and deprioritized", target.ServerID, target.ToolName)
		}
	}
	spec.Preferred = preferred
	spec.Deprioritized = deprioritized
	payload, err := json.Marshal(spec)
	if err != nil {
		return RuleSpec{}, "", "", "", nil, err
	}
	ruleID := "pref:" + strings.TrimPrefix(toolcontract.FingerprintJSON(payload), "sha256:")
	conflictBody, _ := json.Marshal(struct {
		Specificity Specificity `json:"specificity"`
		SubjectKey  string      `json:"subject_key"`
		Condition   string      `json:"condition,omitempty"`
	}{spec.Specificity, spec.SubjectKey, spec.Condition})
	conflictKey := "slot:" + strings.TrimPrefix(toolcontract.FingerprintJSON(conflictBody), "sha256:")
	assumptionBody, _ := json.Marshal(struct {
		Preferred     []Target `json:"preferred"`
		Deprioritized []Target `json:"deprioritized,omitempty"`
	}{preferred, deprioritized})
	assumptionFingerprint := toolcontract.FingerprintJSON(assumptionBody)
	return spec, ruleID, conflictKey, assumptionFingerprint, payload, nil
}

func normalizeTargets(targets []Target) ([]Target, error) {
	normalized := append([]Target(nil), targets...)
	for index := range normalized {
		normalized[index].ServerID = strings.TrimSpace(normalized[index].ServerID)
		normalized[index].ToolName = strings.TrimSpace(normalized[index].ToolName)
		normalized[index].AssumptionFingerprint = strings.TrimSpace(normalized[index].AssumptionFingerprint)
		if normalized[index].ServerID == "" || normalized[index].ToolName == "" || normalized[index].AssumptionFingerprint == "" {
			return nil, errors.New("preference targets require server id, tool name, and assumption fingerprint")
		}
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].ServerID == normalized[j].ServerID {
			return normalized[i].ToolName < normalized[j].ToolName
		}
		return normalized[i].ServerID < normalized[j].ServerID
	})
	for index := 1; index < len(normalized); index++ {
		if normalized[index-1].ServerID == normalized[index].ServerID && normalized[index-1].ToolName == normalized[index].ToolName {
			return nil, fmt.Errorf("duplicate preference target %s/%s", normalized[index].ServerID, normalized[index].ToolName)
		}
	}
	return normalized, nil
}

func ruleTargetsChanged(spec RuleSpec, current map[string]string) bool {
	for _, target := range append(append([]Target(nil), spec.Preferred...), spec.Deprioritized...) {
		assumption, ok := current[TargetMapKey(target.ServerID, target.ToolName)]
		if !ok || assumption != target.AssumptionFingerprint {
			return true
		}
	}
	return false
}

func scanRule(row interface{ Scan(...any) error }) (Rule, error) {
	var rule Rule
	var reviewState string
	var payload []byte
	var updatedMS int64
	if err := row.Scan(&rule.ID, &rule.ConflictKey, &rule.AssumptionFingerprint, &reviewState, &payload, &updatedMS); err != nil {
		return Rule{}, err
	}
	if err := json.Unmarshal(payload, &rule.Spec); err != nil {
		return Rule{}, fmt.Errorf("decode routing preference %s: %w", rule.ID, err)
	}
	rule.ReviewState = ReviewState(reviewState)
	rule.UpdatedAt = time.UnixMilli(updatedMS).UTC()
	return rule, nil
}

func preferenceRevisionTx(ctx context.Context, tx *sql.Tx) (uint64, error) {
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT preference_revision FROM routing_state WHERE singleton = 1`).Scan(&raw); err != nil {
		return 0, err
	}
	return parseRevision(raw)
}

func parseRevision(raw string) (uint64, error) {
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse preference revision: %w", err)
	}
	return value, nil
}

func setPreferenceRevisionTx(ctx context.Context, tx *sql.Tx, revision uint64) error {
	result, err := tx.ExecContext(ctx, `UPDATE routing_state SET preference_revision = ? WHERE singleton = 1`, strconv.FormatUint(revision, 10))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("routing state singleton is missing")
	}
	return nil
}

func specificityPriority(value Specificity) int {
	switch value {
	case SpecificityConditionalTool:
		return 3
	case SpecificityToolSet:
		return 2
	case SpecificityServer:
		return 1
	default:
		return 0
	}
}
