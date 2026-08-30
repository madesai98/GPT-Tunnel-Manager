package enrichment

import (
	"bytes"
	"encoding/json"
)

// These protocol responses are agent-authored. Reject unknown properties rather
// than allowing encoding/json to silently discard a misspelled or guessed field
// and surface a misleading downstream semantic validation error.
func strictProtocolDecode(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}

func (r *ToolBatchResponse) UnmarshalJSON(data []byte) error {
	type alias ToolBatchResponse
	var decoded alias
	if err := strictProtocolDecode(data, &decoded); err != nil {
		return err
	}
	*r = ToolBatchResponse(decoded)
	return nil
}

func (r *CapabilityBatchResponse) UnmarshalJSON(data []byte) error {
	type alias CapabilityBatchResponse
	var decoded alias
	if err := strictProtocolDecode(data, &decoded); err != nil {
		return err
	}
	*r = CapabilityBatchResponse(decoded)
	return nil
}

func (r *AmbiguityReviewResponse) UnmarshalJSON(data []byte) error {
	type alias AmbiguityReviewResponse
	var decoded alias
	if err := strictProtocolDecode(data, &decoded); err != nil {
		return err
	}
	*r = AmbiguityReviewResponse(decoded)
	return nil
}
