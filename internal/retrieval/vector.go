package retrieval

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/embedding"
)

type VectorRecord struct {
	Key    string
	Vector []float32
}

type VectorResult struct {
	Key   string
	Score float64
}

type VectorIndex struct {
	dimensions int
	records    []VectorRecord
}

func NewVectorIndex(records []VectorRecord) (*VectorIndex, error) {
	if len(records) == 0 {
		return &VectorIndex{}, nil
	}
	dimensions := len(records[0].Vector)
	if dimensions == 0 {
		return nil, fmt.Errorf("vector record %q: %w", records[0].Key, embedding.ErrInvalidVector)
	}
	seen := make(map[string]struct{}, len(records))
	normalized := make([]VectorRecord, 0, len(records))
	for _, record := range records {
		if record.Key == "" {
			return nil, errors.New("vector record key is required")
		}
		if _, ok := seen[record.Key]; ok {
			return nil, fmt.Errorf("duplicate vector record key %q", record.Key)
		}
		seen[record.Key] = struct{}{}
		if err := embedding.ValidateVector(record.Vector, dimensions); err != nil {
			return nil, fmt.Errorf("vector record %q: %w", record.Key, err)
		}
		unit, err := normalizeVector(record.Vector)
		if err != nil {
			return nil, fmt.Errorf("vector record %q: %w", record.Key, err)
		}
		normalized = append(normalized, VectorRecord{Key: record.Key, Vector: unit})
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Key < normalized[j].Key })
	return &VectorIndex{dimensions: dimensions, records: normalized}, nil
}

func (i *VectorIndex) Dimensions() int {
	if i == nil {
		return 0
	}
	return i.dimensions
}

func (i *VectorIndex) Len() int {
	if i == nil {
		return 0
	}
	return len(i.records)
}

func (i *VectorIndex) Keys() []string {
	if i == nil {
		return nil
	}
	keys := make([]string, len(i.records))
	for index, record := range i.records {
		keys[index] = record.Key
	}
	return keys
}

func (i *VectorIndex) Neighbors(key string, limit int) ([]VectorResult, error) {
	if i == nil {
		return nil, errors.New("vector index is nil")
	}
	if limit < 0 {
		return nil, errors.New("vector neighbor limit cannot be negative")
	}
	if limit == 0 || len(i.records) <= 1 {
		return []VectorResult{}, nil
	}
	position := sort.Search(len(i.records), func(index int) bool { return i.records[index].Key >= key })
	if position >= len(i.records) || i.records[position].Key != key {
		return nil, fmt.Errorf("vector record %q not found", key)
	}
	results, err := i.Search(i.records[position].Vector, limit+1)
	if err != nil {
		return nil, err
	}
	neighbors := make([]VectorResult, 0, limit)
	for _, result := range results {
		if result.Key == key {
			continue
		}
		neighbors = append(neighbors, result)
		if len(neighbors) == limit {
			break
		}
	}
	return neighbors, nil
}

func (i *VectorIndex) Search(query []float32, limit int) ([]VectorResult, error) {
	if i == nil {
		return nil, errors.New("vector index is nil")
	}
	if limit < 0 {
		return nil, errors.New("vector search limit cannot be negative")
	}
	if len(i.records) == 0 || limit == 0 {
		return []VectorResult{}, nil
	}
	if err := embedding.ValidateVector(query, i.dimensions); err != nil {
		return nil, fmt.Errorf("query vector: %w", err)
	}
	unitQuery, err := normalizeVector(query)
	if err != nil {
		return nil, err
	}
	results := make([]VectorResult, 0, len(i.records))
	for _, record := range i.records {
		var score float64
		for dimension, value := range record.Vector {
			score += float64(value) * float64(unitQuery[dimension])
		}
		if score > 1 && score < 1+1e-6 {
			score = 1
		} else if score < -1 && score > -1-1e-6 {
			score = -1
		}
		results = append(results, VectorResult{Key: record.Key, Score: score})
	}
	sort.Slice(results, func(left, right int) bool {
		if results[left].Score == results[right].Score {
			return results[left].Key < results[right].Key
		}
		return results[left].Score > results[right].Score
	})
	if limit < len(results) {
		results = results[:limit]
	}
	return results, nil
}

func normalizeVector(vector []float32) ([]float32, error) {
	var squared float64
	for _, value := range vector {
		squared += float64(value) * float64(value)
	}
	if squared == 0 {
		return nil, embedding.ErrZeroVector
	}
	magnitude := math.Sqrt(squared)
	result := make([]float32, len(vector))
	for index, value := range vector {
		result[index] = float32(float64(value) / magnitude)
	}
	return result, nil
}
