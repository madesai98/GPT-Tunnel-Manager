package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// CachedTool is a generation-independent copy of a tool contract. Availability
// is deliberately separate from the contract identity so bridge-style MCP
// servers can temporarily hide tools without erasing their semantic history.
type CachedTool struct {
	ServerID          string
	ToolName          string
	SourceFingerprint string
	ContractJSON      []byte
	Available         bool
	LastSeenAtUnixMS  int64
}

// ToolObservationResult describes what changed when a live tools/list snapshot
// was reconciled with the persistent contract cache.
type ToolObservationResult struct {
	SemanticChanged     bool
	AvailabilityChanged bool
}

// ObserveServerTools records the currently advertised tools for one server.
// Missing tools are marked unavailable, never deleted. A tool only counts as a
// semantic change when an authoritative active/staging generation already
// exists and a tool is new or its canonical contract fingerprint changed.
func (c *Catalog) ObserveServerTools(ctx context.Context, serverID string, tools []*mcp.Tool) (ToolObservationResult, error) {
	serverID = strings.TrimSpace(serverID)
	if serverID == "" {
		return ToolObservationResult{}, errors.New("server id is required")
	}

	type observedTool struct {
		name        string
		fingerprint string
		body        []byte
	}
	observed := make(map[string]observedTool, len(tools))
	for _, tool := range tools {
		if tool == nil || strings.TrimSpace(tool.Name) == "" {
			return ToolObservationResult{}, errors.New("observed tool requires a name")
		}
		if _, exists := observed[tool.Name]; exists {
			return ToolObservationResult{}, fmt.Errorf("duplicate observed tool name %q", tool.Name)
		}
		fingerprint, body, err := toolcontract.FingerprintTool(tool)
		if err != nil {
			return ToolObservationResult{}, err
		}
		observed[tool.Name] = observedTool{name: tool.Name, fingerprint: fingerprint, body: body}
	}

	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return ToolObservationResult{}, fmt.Errorf("begin tool observation transaction: %w", err)
	}
	rollback := func(cause error) (ToolObservationResult, error) {
		_ = tx.Rollback()
		return ToolObservationResult{}, cause
	}

	var authoritativeBaseline int
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM source_servers ss
			JOIN generations g ON g.generation_id = ss.generation_id
			WHERE ss.server_id = ? AND g.status IN ('active', 'staging')
		)
	`, serverID).Scan(&authoritativeBaseline); err != nil {
		return rollback(fmt.Errorf("inspect tool cache baseline for %s: %w", serverID, err))
	}

	// Upgrade compatibility: seed the new persistent cache from authoritative
	// generation data before interpreting a partial live snapshot. Prefer the
	// newest staging contract because it can be newer than active, then fill any
	// missing tools from active. This prevents an upgrade while the paired app is
	// closed from discarding either staged changes or previously active tools.
	now := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO tool_contract_cache(
			server_id, tool_name, source_fingerprint, contract_json, available, last_seen_at_unix_ms
		)
		SELECT st.server_id, st.tool_name, st.source_fingerprint, st.contract_json, 1, ?
		FROM source_tools st
		JOIN generations g ON g.generation_id = st.generation_id
		WHERE st.server_id = ? AND g.status = 'staging'
		ORDER BY g.created_at_unix_ms DESC
	`, now, serverID); err != nil {
		return rollback(fmt.Errorf("seed staging tool cache for %s: %w", serverID, err))
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO tool_contract_cache(
			server_id, tool_name, source_fingerprint, contract_json, available, last_seen_at_unix_ms
		)
		SELECT st.server_id, st.tool_name, st.source_fingerprint, st.contract_json, 1, ?
		FROM source_tools st
		JOIN generations g ON g.generation_id = st.generation_id
		WHERE st.server_id = ? AND g.status = 'active'
	`, now, serverID); err != nil {
		return rollback(fmt.Errorf("seed active tool cache for %s: %w", serverID, err))
	}

	type storedTool struct {
		fingerprint string
		available   bool
	}
	stored := make(map[string]storedTool)
	rows, err := tx.QueryContext(ctx, `
		SELECT tool_name, source_fingerprint, available
		FROM tool_contract_cache WHERE server_id = ?
	`, serverID)
	if err != nil {
		return rollback(fmt.Errorf("load tool cache for %s: %w", serverID, err))
	}
	for rows.Next() {
		var name, fingerprint string
		var available int
		if err := rows.Scan(&name, &fingerprint, &available); err != nil {
			rows.Close()
			return rollback(fmt.Errorf("scan tool cache for %s: %w", serverID, err))
		}
		stored[name] = storedTool{fingerprint: fingerprint, available: available != 0}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return rollback(fmt.Errorf("iterate tool cache for %s: %w", serverID, err))
	}
	rows.Close()

	result := ToolObservationResult{}
	for name, tool := range observed {
		previous, existed := stored[name]
		if authoritativeBaseline != 0 && (!existed || previous.fingerprint != tool.fingerprint) {
			result.SemanticChanged = true
		}
		if !existed || !previous.available {
			result.AvailabilityChanged = true
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tool_contract_cache(
				server_id, tool_name, source_fingerprint, contract_json, available, last_seen_at_unix_ms
			) VALUES (?, ?, ?, ?, 1, ?)
			ON CONFLICT(server_id, tool_name) DO UPDATE SET
				source_fingerprint = excluded.source_fingerprint,
				contract_json = excluded.contract_json,
				available = 1,
				last_seen_at_unix_ms = excluded.last_seen_at_unix_ms
		`, serverID, name, tool.fingerprint, tool.body, now); err != nil {
			return rollback(fmt.Errorf("store observed tool %s/%s: %w", serverID, name, err))
		}
	}

	for name, previous := range stored {
		if _, seen := observed[name]; seen {
			continue
		}
		if previous.available {
			result.AvailabilityChanged = true
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE tool_contract_cache
			SET available = 0
			WHERE server_id = ? AND tool_name = ?
		`, serverID, name); err != nil {
			return rollback(fmt.Errorf("mark cached tool unavailable %s/%s: %w", serverID, name, err))
		}
	}

	if err := tx.Commit(); err != nil {
		return ToolObservationResult{}, fmt.Errorf("commit tool observation for %s: %w", serverID, err)
	}
	return result, nil
}

func (c *Catalog) CachedTools(ctx context.Context, serverID string) ([]CachedTool, error) {
	serverID = strings.TrimSpace(serverID)
	if serverID == "" {
		return nil, errors.New("server id is required")
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT tool_name, source_fingerprint, contract_json, available, last_seen_at_unix_ms
		FROM tool_contract_cache
		WHERE server_id = ?
		ORDER BY tool_name
	`, serverID)
	if err != nil {
		return nil, fmt.Errorf("load cached tools for %s: %w", serverID, err)
	}
	defer rows.Close()
	out := make([]CachedTool, 0)
	for rows.Next() {
		var record CachedTool
		var available int
		record.ServerID = serverID
		if err := rows.Scan(&record.ToolName, &record.SourceFingerprint, &record.ContractJSON, &available, &record.LastSeenAtUnixMS); err != nil {
			return nil, fmt.Errorf("scan cached tools for %s: %w", serverID, err)
		}
		record.Available = available != 0
		if actual := toolcontract.FingerprintJSON(record.ContractJSON); actual != record.SourceFingerprint {
			return nil, fmt.Errorf("%w: cached tool %s/%s fingerprint mismatch", ErrSourceFingerprintMismatch, serverID, record.ToolName)
		}
		var identity struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(record.ContractJSON, &identity); err != nil || identity.Name != record.ToolName {
			return nil, fmt.Errorf("%w: cached tool %s/%s invocation identity mismatch", ErrCatalogCorrupt, serverID, record.ToolName)
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cached tools for %s: %w", serverID, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ToolName < out[j].ToolName })
	return out, nil
}

// ToolAvailability reports whether the exact indexed contract is currently
// advertised. If no live/cache observation exists yet, known is false and the
// caller should preserve legacy behavior rather than hiding the tool.
func (c *Catalog) ToolAvailability(ctx context.Context, serverID, toolName, expectedFingerprint string) (available bool, known bool, err error) {
	var storedFingerprint string
	var storedAvailable int
	err = c.db.QueryRowContext(ctx, `
		SELECT source_fingerprint, available
		FROM tool_contract_cache
		WHERE server_id = ? AND tool_name = ?
	`, serverID, toolName).Scan(&storedFingerprint, &storedAvailable)
	if errors.Is(err, sql.ErrNoRows) {
		return true, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("load cached tool availability %s/%s: %w", serverID, toolName, err)
	}
	if expectedFingerprint != "" && storedFingerprint != expectedFingerprint {
		return false, true, nil
	}
	return storedAvailable != 0, true, nil
}
