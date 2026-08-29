package retrieval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	SourceDescriptionProjectionVersion = "source-description/v1"
	InputSchemaProjectionVersion       = "input-schema/v1"
	LexicalProjectionVersion           = "source-lexical/v1"
)

type Projection struct {
	Version     string
	Text        string
	Fingerprint string
}

type ToolProjections struct {
	SourceDescription Projection
	InputSchema       Projection
	Lexical           Projection
}

func ProjectTool(tool *mcp.Tool) (ToolProjections, error) {
	if tool == nil {
		return ToolProjections{}, errors.New("tool is required")
	}
	if strings.TrimSpace(tool.Name) == "" {
		return ToolProjections{}, errors.New("tool name is required")
	}
	contractJSON, err := json.Marshal(tool)
	if err != nil {
		return ToolProjections{}, fmt.Errorf("marshal tool for projection: %w", err)
	}
	var contract map[string]json.RawMessage
	if err := json.Unmarshal(contractJSON, &contract); err != nil {
		return ToolProjections{}, fmt.Errorf("decode tool for projection: %w", err)
	}
	name := readJSONString(contract, "name")
	title := readJSONString(contract, "title")
	description := readJSONString(contract, "description")

	var descriptionBuilder strings.Builder
	descriptionBuilder.WriteString("name: ")
	descriptionBuilder.WriteString(name)
	if title != "" {
		descriptionBuilder.WriteString("\ntitle: ")
		descriptionBuilder.WriteString(title)
	}
	if description != "" {
		descriptionBuilder.WriteString("\ndescription: ")
		descriptionBuilder.WriteString(description)
	}
	descriptionText := descriptionBuilder.String()

	inputSchema := contract["inputSchema"]
	if len(inputSchema) == 0 {
		inputSchema = contract["input_schema"]
	}
	if len(inputSchema) == 0 {
		inputSchema = []byte("{}")
	}
	var canonicalSchema any
	if err := json.Unmarshal(inputSchema, &canonicalSchema); err != nil {
		return ToolProjections{}, fmt.Errorf("decode input schema projection: %w", err)
	}
	schemaJSON, err := json.Marshal(canonicalSchema)
	if err != nil {
		return ToolProjections{}, fmt.Errorf("encode input schema projection: %w", err)
	}
	schemaText := "input_schema: " + string(schemaJSON)
	lexicalText := descriptionText + "\n" + schemaText

	return ToolProjections{
		SourceDescription: newProjection(SourceDescriptionProjectionVersion, descriptionText),
		InputSchema:       newProjection(InputSchemaProjectionVersion, schemaText),
		Lexical:           newProjection(LexicalProjectionVersion, lexicalText),
	}, nil
}

func readJSONString(values map[string]json.RawMessage, key string) string {
	body := values[key]
	if len(body) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(body, &value); err != nil {
		return ""
	}
	return value
}

func newProjection(version, text string) Projection {
	digest := sha256.Sum256([]byte(version + "\x00" + text))
	return Projection{Version: version, Text: text, Fingerprint: "sha256:" + hex.EncodeToString(digest[:])}
}

func fingerprintText(text string) string {
	digest := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(digest[:])
}
