package toolcontract

import (
	"encoding/json"
	"testing"
)

func TestExecutorClassForJSONNormalizesAllAnnotationClasses(t *testing.T) {
	tests := []struct {
		name        string
		readOnly    bool
		destructive bool
		idempotent  bool
		openWorld   bool
		want        ExecutorClass
	}{
		{"read closed", true, true, false, false, ExecutorReadOnlyClosed},
		{"read open", true, true, false, true, ExecutorReadOnlyOpen},
		{"add closed", false, false, false, false, ExecutorAdditiveClosed},
		{"add closed idem", false, false, true, false, ExecutorAdditiveClosedIdempotent},
		{"add open", false, false, false, true, ExecutorAdditiveOpen},
		{"add open idem", false, false, true, true, ExecutorAdditiveOpenIdempotent},
		{"destructive closed", false, true, false, false, ExecutorDestructiveClosed},
		{"destructive closed idem", false, true, true, false, ExecutorDestructiveClosedIdempotent},
		{"destructive open", false, true, false, true, ExecutorDestructiveOpen},
		{"destructive open idem", false, true, true, true, ExecutorDestructiveOpenIdempotent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"name": "tool", "inputSchema": map[string]any{"type": "object"},
				"annotations": map[string]any{"readOnlyHint": test.readOnly, "destructiveHint": test.destructive, "idempotentHint": test.idempotent, "openWorldHint": test.openWorld},
			})
			got, err := ExecutorClassForJSON(body)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("class = %s, want %s", got, test.want)
			}
		})
	}
	defaultClass, err := ExecutorClassForJSON([]byte(`{"name":"tool","inputSchema":{"type":"object"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if defaultClass != ExecutorDestructiveOpen {
		t.Fatalf("default class = %s", defaultClass)
	}
}

func TestSemanticSourceFingerprintExcludesOperationalMetadata(t *testing.T) {
	base := []byte(`{"name":"tool","title":"Tool","description":"does work","inputSchema":{"type":"object"},"icons":[{"src":"a"}],"_meta":{"opaque":"one"}}`)
	changedMetadata := []byte(`{"name":"tool","title":"Tool","description":"does work","inputSchema":{"type":"object"},"icons":[{"src":"b"}],"_meta":{"opaque":"two"}}`)
	changedSemantic := []byte(`{"name":"tool","title":"Tool","description":"does different work","inputSchema":{"type":"object"},"icons":[{"src":"b"}],"_meta":{"opaque":"two"}}`)
	first, err := SemanticSourceFingerprintJSON(base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SemanticSourceFingerprintJSON(changedMetadata)
	if err != nil {
		t.Fatal(err)
	}
	third, err := SemanticSourceFingerprintJSON(changedSemantic)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("operational metadata changed semantic fingerprint: %s != %s", first, second)
	}
	if first == third {
		t.Fatal("description change did not change semantic fingerprint")
	}
}
