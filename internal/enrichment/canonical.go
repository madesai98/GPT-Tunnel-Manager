package enrichment

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/toolcontract"
)

// canonicalJSONFingerprint mirrors the catalog's immutable enrichment-batch
// canonicalization. Artifact identity derived from a batch request must use the
// same canonical representation that is persisted, otherwise nested RawMessage
// objects can change key ordering between prepare and submit.
func canonicalJSONFingerprint(body []byte) (string, error) {
	if !json.Valid(body) {
		return "", errors.New("invalid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return toolcontract.FingerprintJSON(canonical), nil
}
