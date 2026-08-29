package retrieval

import (
	"errors"
	"math"
	"sort"
	"strings"
	"unicode"
)

const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

type LexicalDocument struct {
	Key  string
	Text string
}

type LexicalResult struct {
	Key   string
	Score float64
}

type lexicalDocument struct {
	key        string
	termCounts map[string]int
	termCount  int
}

type LexicalIndex struct {
	documents         []lexicalDocument
	documentFrequency map[string]int
	averageLength     float64
}

func NewLexicalIndex(documents []LexicalDocument) (*LexicalIndex, error) {
	seen := make(map[string]struct{}, len(documents))
	index := &LexicalIndex{documentFrequency: make(map[string]int)}
	var totalTerms int
	for _, document := range documents {
		if document.Key == "" {
			return nil, errors.New("lexical document key is required")
		}
		if _, ok := seen[document.Key]; ok {
			return nil, errors.New("duplicate lexical document key: " + document.Key)
		}
		seen[document.Key] = struct{}{}
		tokens := Tokenize(document.Text)
		counts := make(map[string]int, len(tokens))
		for _, token := range tokens {
			counts[token]++
		}
		for token := range counts {
			index.documentFrequency[token]++
		}
		index.documents = append(index.documents, lexicalDocument{
			key:        document.Key,
			termCounts: counts,
			termCount:  len(tokens),
		})
		totalTerms += len(tokens)
	}
	sort.Slice(index.documents, func(i, j int) bool { return index.documents[i].key < index.documents[j].key })
	if len(index.documents) > 0 {
		index.averageLength = float64(totalTerms) / float64(len(index.documents))
	}
	return index, nil
}

func (i *LexicalIndex) Len() int {
	if i == nil {
		return 0
	}
	return len(i.documents)
}

func (i *LexicalIndex) Search(query string, limit int) ([]LexicalResult, error) {
	if i == nil {
		return nil, errors.New("lexical index is nil")
	}
	if limit < 0 {
		return nil, errors.New("lexical search limit cannot be negative")
	}
	if limit == 0 || len(i.documents) == 0 {
		return []LexicalResult{}, nil
	}
	queryTokens := Tokenize(query)
	if len(queryTokens) == 0 {
		return []LexicalResult{}, nil
	}
	uniqueQueryTerms := make(map[string]struct{}, len(queryTokens))
	for _, token := range queryTokens {
		uniqueQueryTerms[token] = struct{}{}
	}
	terms := make([]string, 0, len(uniqueQueryTerms))
	for token := range uniqueQueryTerms {
		terms = append(terms, token)
	}
	sort.Strings(terms)

	results := make([]LexicalResult, 0, len(i.documents))
	for _, document := range i.documents {
		var score float64
		for _, term := range terms {
			frequency := document.termCounts[term]
			if frequency == 0 {
				continue
			}
			docFrequency := i.documentFrequency[term]
			idf := math.Log(1 + (float64(len(i.documents)-docFrequency)+0.5)/(float64(docFrequency)+0.5))
			lengthRatio := 0.0
			if i.averageLength > 0 {
				lengthRatio = float64(document.termCount) / i.averageLength
			}
			denominator := float64(frequency) + bm25K1*(1-bm25B+bm25B*lengthRatio)
			score += idf * (float64(frequency) * (bm25K1 + 1)) / denominator
		}
		if score > 0 {
			results = append(results, LexicalResult{Key: document.key, Score: score})
		}
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

func Tokenize(text string) []string {
	var tokens []string
	var builder strings.Builder
	flush := func() {
		if builder.Len() == 0 {
			return
		}
		tokens = append(tokens, builder.String())
		builder.Reset()
	}
	for _, value := range strings.ToLower(text) {
		if unicode.IsLetter(value) || unicode.IsDigit(value) {
			builder.WriteRune(value)
			continue
		}
		flush()
	}
	flush()
	return tokens
}
