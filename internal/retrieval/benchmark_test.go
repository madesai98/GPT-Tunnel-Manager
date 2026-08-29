package retrieval

import (
	"fmt"
	"testing"
)

func BenchmarkExactCosineSearch(b *testing.B) {
	for _, count := range []int{1000, 5000, 10000} {
		b.Run(fmt.Sprintf("tools_%d", count), func(b *testing.B) {
			const dimensions = 256
			records := make([]VectorRecord, count)
			for index := 0; index < count; index++ {
				vector := make([]float32, dimensions)
				for dimension := 0; dimension < dimensions; dimension++ {
					vector[dimension] = float32(((index+1)*(dimension+3))%97+1) / 97
				}
				records[index] = VectorRecord{Key: fmt.Sprintf("tool-%05d", index), Vector: vector}
			}
			index, err := NewVectorIndex(records)
			if err != nil {
				b.Fatal(err)
			}
			query := make([]float32, dimensions)
			for dimension := range query {
				query[dimension] = float32((dimension*7)%89+1) / 89
			}
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				if _, err := index.Search(query, 20); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkLexicalSearch(b *testing.B) {
	for _, count := range []int{1000, 5000, 10000} {
		b.Run(fmt.Sprintf("tools_%d", count), func(b *testing.B) {
			documents := make([]LexicalDocument, count)
			for index := 0; index < count; index++ {
				documents[index] = LexicalDocument{
					Key:  fmt.Sprintf("tool-%05d", index),
					Text: fmt.Sprintf("tool category%d action%d resource%d common routing semantic input output", index%37, index%113, index%251),
				}
			}
			index, err := NewLexicalIndex(documents)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				if _, err := index.Search("category7 action19 semantic", 20); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
