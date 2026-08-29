package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

// SourceServerContract is the authoritative server context permitted to enter
// the catalog. It intentionally excludes endpoints, environment values,
// credential references, runtime tuning, and logging configuration.
type SourceServerContract struct {
	ServerID      string                 `json:"server_id"`
	Name          string                 `json:"name"`
	Mode          v2config.ServerMode    `json:"mode"`
	TransportType v2config.TransportType `json:"transport_type"`
}

type SourceServer struct {
	Contract    SourceServerContract
	Fingerprint string
}

func CanonicalSourceServer(entry v2config.ServerEntry) (SourceServerContract, error) {
	if entry.ID == "" {
		return SourceServerContract{}, errors.New("server id is required")
	}
	if entry.Name == "" {
		return SourceServerContract{}, errors.New("server name is required")
	}
	if entry.Transport.Type == "" {
		return SourceServerContract{}, errors.New("server transport type is required")
	}
	return SourceServerContract{
		ServerID:      entry.ID,
		Name:          entry.Name,
		Mode:          entry.Mode,
		TransportType: entry.Transport.Type,
	}, nil
}

func fingerprintSourceServer(contract SourceServerContract) (string, []byte, error) {
	body, err := json.Marshal(contract)
	if err != nil {
		return "", nil, fmt.Errorf("marshal source server contract %q: %w", contract.ServerID, err)
	}
	return toolcontract.FingerprintJSON(body), body, nil
}

func (c *Catalog) PutSourceServer(ctx context.Context, generationID string, entry v2config.ServerEntry) (SourceServer, error) {
	if generationID == "" {
		return SourceServer{}, errors.New("generation id is required")
	}
	if err := c.requireStaging(ctx, generationID); err != nil {
		return SourceServer{}, err
	}
	contract, err := CanonicalSourceServer(entry)
	if err != nil {
		return SourceServer{}, err
	}
	fingerprint, body, err := fingerprintSourceServer(contract)
	if err != nil {
		return SourceServer{}, err
	}
	_, err = c.db.ExecContext(ctx, `
		INSERT INTO source_servers(generation_id, server_id, source_fingerprint, contract_json)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(generation_id, server_id) DO UPDATE SET
			source_fingerprint = excluded.source_fingerprint,
			contract_json = excluded.contract_json
	`, generationID, contract.ServerID, fingerprint, body)
	if err != nil {
		return SourceServer{}, fmt.Errorf("store source server %s: %w", contract.ServerID, err)
	}
	return SourceServer{Contract: contract, Fingerprint: fingerprint}, nil
}

func (c *Catalog) SourceServer(ctx context.Context, generationID, serverID string) (SourceServer, error) {
	var storedFingerprint string
	var body []byte
	if err := c.db.QueryRowContext(ctx, `
		SELECT source_fingerprint, contract_json
		FROM source_servers WHERE generation_id = ? AND server_id = ?
	`, generationID, serverID).Scan(&storedFingerprint, &body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SourceServer{}, sql.ErrNoRows
		}
		return SourceServer{}, fmt.Errorf("load source server %s: %w", serverID, err)
	}
	var contract SourceServerContract
	if err := json.Unmarshal(body, &contract); err != nil {
		return SourceServer{}, fmt.Errorf("%w: decode source server %s: %v", ErrCatalogCorrupt, serverID, err)
	}
	actualFingerprint := toolcontract.FingerprintJSON(body)
	if actualFingerprint != storedFingerprint {
		return SourceServer{}, fmt.Errorf("%w: source server %s fingerprint mismatch", ErrSourceFingerprintMismatch, serverID)
	}
	return SourceServer{Contract: contract, Fingerprint: storedFingerprint}, nil
}
