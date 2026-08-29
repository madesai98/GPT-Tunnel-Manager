package toolcontract

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ExecutorClass string

const (
	ExecutorReadOnlyClosed              ExecutorClass = "call_read_only_closed"
	ExecutorReadOnlyOpen                ExecutorClass = "call_read_only_open"
	ExecutorAdditiveClosed              ExecutorClass = "call_additive_closed"
	ExecutorAdditiveClosedIdempotent    ExecutorClass = "call_additive_closed_idempotent"
	ExecutorAdditiveOpen                ExecutorClass = "call_additive_open"
	ExecutorAdditiveOpenIdempotent      ExecutorClass = "call_additive_open_idempotent"
	ExecutorDestructiveClosed           ExecutorClass = "call_destructive_closed"
	ExecutorDestructiveClosedIdempotent ExecutorClass = "call_destructive_closed_idempotent"
	ExecutorDestructiveOpen             ExecutorClass = "call_destructive_open"
	ExecutorDestructiveOpenIdempotent   ExecutorClass = "call_destructive_open_idempotent"
)

// ExecutorClassForTool normalizes MCP ToolAnnotations with the protocol
// defaults. The class is derived only from the authoritative source contract;
// enrichment and Routing Preferences never participate in classification.
func ExecutorClassForTool(tool *mcp.Tool) (ExecutorClass, error) {
	if tool == nil {
		return "", errors.New("tool is required")
	}
	body, err := json.Marshal(tool)
	if err != nil {
		return "", fmt.Errorf("marshal tool for executor classification: %w", err)
	}
	return ExecutorClassForJSON(body)
}

// ExecutorClassForJSON classifies a canonical downstream tool contract without
// depending on the Go SDK's pointer/value representation of optional hints.
func ExecutorClassForJSON(contractJSON []byte) (ExecutorClass, error) {
	if !json.Valid(contractJSON) {
		return "", errors.New("tool contract is invalid JSON")
	}
	var contract map[string]json.RawMessage
	if err := json.Unmarshal(contractJSON, &contract); err != nil {
		return "", fmt.Errorf("decode tool contract: %w", err)
	}
	var name string
	if raw := contract["name"]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &name); err != nil {
			return "", errors.New("tool contract name is invalid")
		}
	}
	if strings.TrimSpace(name) == "" {
		return "", errors.New("tool contract name is required")
	}

	readOnly := false
	destructive := true
	idempotent := false
	openWorld := true
	if raw := contract["annotations"]; len(raw) != 0 && string(raw) != "null" {
		var annotations map[string]json.RawMessage
		if err := json.Unmarshal(raw, &annotations); err != nil {
			return "", errors.New("tool annotations are invalid")
		}
		var err error
		if readOnly, err = boolHint(annotations, "readOnlyHint", readOnly); err != nil {
			return "", err
		}
		if destructive, err = boolHint(annotations, "destructiveHint", destructive); err != nil {
			return "", err
		}
		if idempotent, err = boolHint(annotations, "idempotentHint", idempotent); err != nil {
			return "", err
		}
		if openWorld, err = boolHint(annotations, "openWorldHint", openWorld); err != nil {
			return "", err
		}
	}
	if readOnly {
		if openWorld {
			return ExecutorReadOnlyOpen, nil
		}
		return ExecutorReadOnlyClosed, nil
	}
	if destructive {
		if openWorld {
			if idempotent {
				return ExecutorDestructiveOpenIdempotent, nil
			}
			return ExecutorDestructiveOpen, nil
		}
		if idempotent {
			return ExecutorDestructiveClosedIdempotent, nil
		}
		return ExecutorDestructiveClosed, nil
	}
	if openWorld {
		if idempotent {
			return ExecutorAdditiveOpenIdempotent, nil
		}
		return ExecutorAdditiveOpen, nil
	}
	if idempotent {
		return ExecutorAdditiveClosedIdempotent, nil
	}
	return ExecutorAdditiveClosed, nil
}

// SemanticSourceFingerprintJSON fingerprints only source fields that can change
// semantic routing assumptions. Operational/display-only metadata such as icons
// and _meta is deliberately excluded; executor-affecting annotation hints are
// bound separately through ExecutorClassForJSON.
func SemanticSourceFingerprintJSON(contractJSON []byte) (string, error) {
	if !json.Valid(contractJSON) {
		return "", errors.New("tool contract is invalid JSON")
	}
	var contract map[string]json.RawMessage
	if err := json.Unmarshal(contractJSON, &contract); err != nil {
		return "", fmt.Errorf("decode tool contract: %w", err)
	}
	var annotationTitle json.RawMessage
	if raw := contract["annotations"]; len(raw) != 0 && string(raw) != "null" {
		var annotations map[string]json.RawMessage
		if err := json.Unmarshal(raw, &annotations); err != nil {
			return "", errors.New("tool annotations are invalid")
		}
		annotationTitle = annotations["title"]
	}
	type semanticSource struct {
		Name            any `json:"name"`
		Title           any `json:"title,omitempty"`
		Description     any `json:"description,omitempty"`
		InputSchema     any `json:"input_schema"`
		OutputSchema    any `json:"output_schema,omitempty"`
		AnnotationTitle any `json:"annotation_title,omitempty"`
	}
	read := func(key string, required bool) (any, error) {
		raw := contract[key]
		if len(raw) == 0 {
			if required {
				return nil, fmt.Errorf("tool contract %s is required", key)
			}
			return nil, nil
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("tool contract %s is invalid: %w", key, err)
		}
		return value, nil
	}
	name, err := read("name", true)
	if err != nil {
		return "", err
	}
	title, err := read("title", false)
	if err != nil {
		return "", err
	}
	description, err := read("description", false)
	if err != nil {
		return "", err
	}
	input, err := read("inputSchema", true)
	if err != nil {
		input, err = read("input_schema", true)
	}
	if err != nil {
		return "", err
	}
	output, err := read("outputSchema", false)
	if err != nil {
		return "", err
	}
	if output == nil {
		output, err = read("output_schema", false)
		if err != nil {
			return "", err
		}
	}
	var annotationTitleValue any
	if len(annotationTitle) != 0 {
		if err := json.Unmarshal(annotationTitle, &annotationTitleValue); err != nil {
			return "", errors.New("tool annotation title is invalid")
		}
	}
	body, err := json.Marshal(semanticSource{
		Name: name, Title: title, Description: description, InputSchema: input,
		OutputSchema: output, AnnotationTitle: annotationTitleValue,
	})
	if err != nil {
		return "", fmt.Errorf("marshal semantic source identity: %w", err)
	}
	return FingerprintJSON(body), nil
}

func boolHint(values map[string]json.RawMessage, key string, defaultValue bool) (bool, error) {
	raw, ok := values[key]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return defaultValue, nil
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("tool annotation %s must be boolean", key)
	}
	return value, nil
}
