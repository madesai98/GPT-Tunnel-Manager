package discovery

import (
	"errors"
	"sort"
)

const defaultRRFK = 60

type RankedFacet struct {
	Name   string
	Weight float64
	Keys   []string
}

type fusedResult struct {
	Key   string
	Score float64
}

func reciprocalRankFusion(facets []RankedFacet, k int) ([]fusedResult, error) {
	if k <= 0 {
		return nil, errors.New("rrf k must be positive")
	}
	scores := map[string]float64{}
	for _, facet := range facets {
		if facet.Weight < 0 {
			return nil, errors.New("rrf facet weight cannot be negative")
		}
		if facet.Weight == 0 {
			continue
		}
		seen := make(map[string]struct{}, len(facet.Keys))
		for index, key := range facet.Keys {
			if key == "" {
				return nil, errors.New("rrf facet contains empty key")
			}
			if _, duplicate := seen[key]; duplicate {
				return nil, errors.New("rrf facet contains duplicate key")
			}
			seen[key] = struct{}{}
			rank := index + 1
			scores[key] += facet.Weight / float64(k+rank)
		}
	}
	results := make([]fusedResult, 0, len(scores))
	for key, score := range scores {
		results = append(results, fusedResult{Key: key, Score: score})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Key < results[j].Key
		}
		return results[i].Score > results[j].Score
	})
	return results, nil
}
