package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
)

type LexicalRecord struct {
	MemberKey   string
	Fingerprint string
	Text        string
}

func (c *Catalog) PutLexicalRecord(ctx context.Context, generationID, memberKey, text string) (LexicalRecord, error) {
	if c == nil || c.db == nil {
		return LexicalRecord{}, errors.New("catalog is closed")
	}
	if strings.TrimSpace(memberKey) == "" {
		return LexicalRecord{}, errors.New("lexical member key is required")
	}
	if err := c.requireStaging(ctx, generationID); err != nil {
		return LexicalRecord{}, err
	}
	fingerprint := toolcontract.FingerprintJSON([]byte(text))
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO lexical_records(generation_id, member_key, lexical_fingerprint, lexical_text)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(generation_id, member_key) DO UPDATE SET
			lexical_fingerprint = excluded.lexical_fingerprint,
			lexical_text = excluded.lexical_text
	`, generationID, memberKey, fingerprint, text)
	if err != nil {
		return LexicalRecord{}, fmt.Errorf("store lexical record %q: %w", memberKey, err)
	}
	return LexicalRecord{MemberKey: memberKey, Fingerprint: fingerprint, Text: text}, nil
}

func (c *Catalog) LexicalRecords(ctx context.Context, generationID string) ([]LexicalRecord, error) {
	if c == nil || c.db == nil {
		return nil, errors.New("catalog is closed")
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT member_key, lexical_fingerprint, lexical_text
		FROM lexical_records
		WHERE generation_id = ?
		ORDER BY member_key
	`, generationID)
	if err != nil {
		return nil, fmt.Errorf("list lexical records: %w", err)
	}
	defer rows.Close()
	var records []LexicalRecord
	for rows.Next() {
		var record LexicalRecord
		if err := rows.Scan(&record.MemberKey, &record.Fingerprint, &record.Text); err != nil {
			return nil, fmt.Errorf("scan lexical record: %w", err)
		}
		if actual := toolcontract.FingerprintJSON([]byte(record.Text)); actual != record.Fingerprint {
			return nil, fmt.Errorf("%w: lexical record %q fingerprint mismatch", ErrCatalogCorrupt, record.MemberKey)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list lexical records: %w", err)
	}
	return records, nil
}
