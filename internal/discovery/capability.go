package discovery

import (
	"math"
	"sort"
	"strings"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/enrichment"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/retrieval"
)

func capabilityVectorRanking(query []float32, hierarchy enrichment.CapabilityHierarchy, enrichedIndex *retrieval.VectorIndex) []scoredKey {
	scores := map[string]float64{}
	seen := map[string]bool{}
	for _, node := range hierarchy.Capabilities {
		if len(node.ToolMembers) == 0 {
			continue
		}
		var centroid []float32
		count := 0
		for _, member := range node.ToolMembers {
			vector, ok := enrichedIndex.Vector(member)
			if !ok {
				continue
			}
			if centroid == nil {
				centroid = make([]float32, len(vector))
			}
			if len(centroid) != len(vector) {
				continue
			}
			for index, value := range vector {
				centroid[index] += value
			}
			count++
		}
		if count == 0 {
			continue
		}
		score := cosine(query, centroid)
		for _, member := range node.ToolMembers {
			if !seen[member] || score > scores[member] {
				scores[member] = score
				seen[member] = true
			}
		}
	}
	results := make([]scoredKey, 0, len(scores))
	for key, score := range scores {
		results = append(results, scoredKey{Key: key, Score: score})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Key < results[j].Key
		}
		return results[i].Score > results[j].Score
	})
	return results
}

func cosine(left, right []float32) float64 {
	if len(left) == 0 || len(left) != len(right) {
		return -1
	}
	var dot, leftNorm, rightNorm float64
	for index := range left {
		l := float64(left[index])
		r := float64(right[index])
		dot += l * r
		leftNorm += l * l
		rightNorm += r * r
	}
	if leftNorm == 0 || rightNorm == 0 {
		return -1
	}
	return dot / (math.Sqrt(leftNorm) * math.Sqrt(rightNorm))
}

func capabilitySummaries(hierarchy enrichment.CapabilityHierarchy) map[string][]CapabilitySummary {
	byID := make(map[string]enrichment.CapabilityNode, len(hierarchy.Capabilities))
	for _, node := range hierarchy.Capabilities {
		byID[node.ID] = node
	}
	result := map[string][]CapabilitySummary{}
	for _, node := range hierarchy.Capabilities {
		for _, member := range node.ToolMembers {
			pathNodes := capabilityPath(node, byID)
			for _, pathNode := range pathNodes {
				path := capabilityPathString(pathNode, byID)
				summary := CapabilitySummary{ID: pathNode.ID, Name: pathNode.Name, Description: pathNode.Description, Path: path}
				if !containsCapability(result[member], summary.ID) {
					result[member] = append(result[member], summary)
				}
			}
		}
	}
	for member := range result {
		sort.Slice(result[member], func(i, j int) bool {
			if result[member][i].Path == result[member][j].Path {
				return result[member][i].ID < result[member][j].ID
			}
			return result[member][i].Path < result[member][j].Path
		})
	}
	return result
}

func capabilityPath(node enrichment.CapabilityNode, byID map[string]enrichment.CapabilityNode) []enrichment.CapabilityNode {
	path := []enrichment.CapabilityNode{node}
	seen := map[string]struct{}{node.ID: {}}
	for node.ParentID != "" {
		parent, ok := byID[node.ParentID]
		if !ok {
			break
		}
		if _, duplicate := seen[parent.ID]; duplicate {
			break
		}
		seen[parent.ID] = struct{}{}
		path = append(path, parent)
		node = parent
	}
	return path
}

func capabilityPathString(node enrichment.CapabilityNode, byID map[string]enrichment.CapabilityNode) string {
	path := capabilityPath(node, byID)
	parts := make([]string, len(path))
	for index := range path {
		parts[len(path)-1-index] = path[index].Name
	}
	return strings.Join(parts, " > ")
}

func containsCapability(items []CapabilitySummary, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
