package retrieval

import (
	"errors"
	"math"
	"testing"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/embedding"
)

func TestVectorIndexExactCosineAndStableTies(t *testing.T) {
	index, err := NewVectorIndex([]VectorRecord{
		{Key: "c", Vector: []float32{1, 0}},
		{Key: "b", Vector: []float32{0, 1}},
		{Key: "a", Vector: []float32{1, 0}},
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := index.Search([]float32{1, 0}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("result count = %d", len(results))
	}
	if results[0].Key != "a" || results[1].Key != "c" || results[2].Key != "b" {
		t.Fatalf("result order = %#v", results)
	}
	if math.Abs(results[0].Score-1) > 1e-6 || math.Abs(results[2].Score) > 1e-6 {
		t.Fatalf("scores = %#v", results)
	}
}

func TestVectorIndexRejectsInvalidVectors(t *testing.T) {
	if _, err := NewVectorIndex([]VectorRecord{{Key: "zero", Vector: []float32{0, 0}}}); !errors.Is(err, embedding.ErrZeroVector) {
		t.Fatalf("zero vector error = %v", err)
	}
	if _, err := NewVectorIndex([]VectorRecord{{Key: "nan", Vector: []float32{float32(math.NaN()), 1}}}); !errors.Is(err, embedding.ErrInvalidVector) {
		t.Fatalf("NaN vector error = %v", err)
	}
	index, err := NewVectorIndex([]VectorRecord{{Key: "ok", Vector: []float32{1, 0}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Search([]float32{1, 0, 0}, 1); !errors.Is(err, embedding.ErrDimensionMismatch) {
		t.Fatalf("dimension error = %v", err)
	}
}
