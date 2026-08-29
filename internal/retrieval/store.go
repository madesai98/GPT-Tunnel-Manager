package retrieval

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/catalog"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/embedding"
)

const (
	EmbeddingArtifactKind       = "retrieval.embedding-vector/v1"
	LexicalArtifactKind         = "retrieval.lexical/v1"
	RoleSourceDescriptionVector = "embedding.source_description"
	RoleInputSchemaVector       = "embedding.input_schema"
	RoleLexical                 = "lexical.source"
	vectorPayloadMagic          = "GTMVEC01"
)

type CatalogStore struct {
	catalog *catalog.Catalog
}

func NewCatalogStore(c *catalog.Catalog) (*CatalogStore, error) {
	if c == nil {
		return nil, errors.New("catalog is required")
	}
	return &CatalogStore{catalog: c}, nil
}

func embeddingSpec(role, memberKey string, identity embedding.Identity, projection Projection) catalog.RequiredArtifactSpec {
	return catalog.RequiredArtifactSpec{
		Role:      role,
		MemberKey: memberKey,
		Kind:      EmbeddingArtifactKind,
		Dependencies: []catalog.ArtifactDependency{
			{Key: "embedding.input", Fingerprint: projection.Fingerprint},
			{Key: "embedding.provider", Fingerprint: identity.Fingerprint()},
			{Key: "embedding.projection", Fingerprint: fingerprintText(projection.Version)},
		},
	}
}

func lexicalSpec(memberKey string, projection Projection) catalog.RequiredArtifactSpec {
	return catalog.RequiredArtifactSpec{
		Role:      RoleLexical,
		MemberKey: memberKey,
		Kind:      LexicalArtifactKind,
		Dependencies: []catalog.ArtifactDependency{
			{Key: "lexical.input", Fingerprint: projection.Fingerprint},
			{Key: "lexical.projection", Fingerprint: fingerprintText(projection.Version)},
		},
	}
}

func (s *CatalogStore) RequireEmbedding(ctx context.Context, generationID, role, memberKey string, identity embedding.Identity, projection Projection) error {
	if err := identity.Validate(); err != nil {
		return err
	}
	_, err := s.catalog.RequireArtifact(ctx, generationID, embeddingSpec(role, memberKey, identity, projection))
	return err
}

func (s *CatalogStore) ReuseEmbedding(ctx context.Context, role, memberKey string, identity embedding.Identity, projection Projection) ([]float32, string, bool, error) {
	spec := embeddingSpec(role, memberKey, identity, projection)
	artifact, err := s.catalog.FindReusableArtifact(ctx, spec.Kind, spec.Dependencies, spec.ContextFingerprint)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", false, nil
		}
		return nil, "", false, err
	}
	vector, err := decodeVectorPayload(artifact.Payload)
	if err != nil {
		return nil, "", false, fmt.Errorf("decode reusable embedding artifact: %w", err)
	}
	expectedDimensions := 0
	if identity.Dimensions != nil {
		expectedDimensions = *identity.Dimensions
	}
	if err := embedding.ValidateVector(vector, expectedDimensions); err != nil {
		return nil, "", false, fmt.Errorf("validate reusable embedding artifact: %w", err)
	}
	return vector, artifact.Key, true, nil
}

func (s *CatalogStore) StoreEmbedding(ctx context.Context, generationID, role, memberKey string, identity embedding.Identity, projection Projection, vector []float32) (string, error) {
	if err := identity.Validate(); err != nil {
		return "", err
	}
	expectedDimensions := 0
	if identity.Dimensions != nil {
		expectedDimensions = *identity.Dimensions
	}
	if err := embedding.ValidateVector(vector, expectedDimensions); err != nil {
		return "", err
	}
	spec := embeddingSpec(role, memberKey, identity, projection)
	payload, err := encodeVectorPayload(vector)
	if err != nil {
		return "", err
	}
	artifact, err := s.catalog.PutArtifact(ctx, catalog.ArtifactSpec{
		Kind:               spec.Kind,
		Payload:            payload,
		Dependencies:       spec.Dependencies,
		ContextFingerprint: spec.ContextFingerprint,
	})
	if err != nil {
		return "", err
	}
	if err := s.catalog.FulfillArtifact(ctx, generationID, spec, artifact.Key); err != nil {
		return "", err
	}
	return artifact.Key, nil
}

func (s *CatalogStore) RequireLexical(ctx context.Context, generationID, memberKey string, projection Projection) error {
	_, err := s.catalog.RequireArtifact(ctx, generationID, lexicalSpec(memberKey, projection))
	return err
}

func (s *CatalogStore) StoreLexical(ctx context.Context, generationID, memberKey string, projection Projection) (string, error) {
	spec := lexicalSpec(memberKey, projection)
	artifact, err := s.catalog.PutArtifact(ctx, catalog.ArtifactSpec{
		Kind:               spec.Kind,
		Payload:            []byte(projection.Text),
		Dependencies:       spec.Dependencies,
		ContextFingerprint: spec.ContextFingerprint,
	})
	if err != nil {
		return "", err
	}
	if _, err := s.catalog.PutLexicalRecord(ctx, generationID, memberKey, projection.Text); err != nil {
		return "", err
	}
	if err := s.catalog.FulfillArtifact(ctx, generationID, spec, artifact.Key); err != nil {
		return "", err
	}
	return artifact.Key, nil
}

func (s *CatalogStore) LoadVectorIndex(ctx context.Context, generationID, role string) (*VectorIndex, error) {
	artifacts, err := s.catalog.GenerationArtifacts(ctx, generationID, role)
	if err != nil {
		return nil, err
	}
	records := make([]VectorRecord, 0, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.Artifact.Kind != EmbeddingArtifactKind {
			return nil, fmt.Errorf("generation artifact %s/%s has kind %q, want %q", artifact.Role, artifact.MemberKey, artifact.Artifact.Kind, EmbeddingArtifactKind)
		}
		vector, err := decodeVectorPayload(artifact.Artifact.Payload)
		if err != nil {
			return nil, fmt.Errorf("decode generation embedding %s: %w", artifact.MemberKey, err)
		}
		records = append(records, VectorRecord{Key: artifact.MemberKey, Vector: vector})
	}
	return NewVectorIndex(records)
}

func (s *CatalogStore) LoadLexicalIndex(ctx context.Context, generationID string) (*LexicalIndex, error) {
	records, err := s.catalog.LexicalRecords(ctx, generationID)
	if err != nil {
		return nil, err
	}
	documents := make([]LexicalDocument, 0, len(records))
	for _, record := range records {
		documents = append(documents, LexicalDocument{Key: record.MemberKey, Text: record.Text})
	}
	return NewLexicalIndex(documents)
}

func encodeVectorPayload(vector []float32) ([]byte, error) {
	if err := embedding.ValidateVector(vector, 0); err != nil {
		return nil, err
	}
	payload := make([]byte, len(vectorPayloadMagic)+4+len(vector)*4)
	copy(payload, vectorPayloadMagic)
	binary.BigEndian.PutUint32(payload[len(vectorPayloadMagic):], uint32(len(vector)))
	offset := len(vectorPayloadMagic) + 4
	for _, value := range vector {
		binary.BigEndian.PutUint32(payload[offset:], math.Float32bits(value))
		offset += 4
	}
	return payload, nil
}

func decodeVectorPayload(payload []byte) ([]float32, error) {
	headerSize := len(vectorPayloadMagic) + 4
	if len(payload) < headerSize || string(payload[:len(vectorPayloadMagic)]) != vectorPayloadMagic {
		return nil, errors.New("invalid embedding artifact payload header")
	}
	dimensions := int(binary.BigEndian.Uint32(payload[len(vectorPayloadMagic):headerSize]))
	if dimensions <= 0 || dimensions > 65536 {
		return nil, errors.New("invalid embedding artifact dimensions")
	}
	if len(payload) != headerSize+dimensions*4 {
		return nil, errors.New("invalid embedding artifact payload length")
	}
	vector := make([]float32, dimensions)
	offset := headerSize
	for index := range vector {
		vector[index] = math.Float32frombits(binary.BigEndian.Uint32(payload[offset:]))
		offset += 4
	}
	if err := embedding.ValidateVector(vector, dimensions); err != nil {
		return nil, err
	}
	return vector, nil
}
