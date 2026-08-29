package discovery

import (
	"strings"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/retrieval"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/routingprefs"
)

func lexicalKeys(results []retrieval.LexicalResult) []string {
	keys := make([]string, len(results))
	for index, result := range results {
		keys[index] = result.Key
	}
	return keys
}

func vectorKeys(results []retrieval.VectorResult) []string {
	keys := make([]string, len(results))
	for index, result := range results {
		keys[index] = result.Key
	}
	return keys
}

func scoredKeys(results []scoredKey) []string {
	keys := make([]string, len(results))
	for index, result := range results {
		keys[index] = result.Key
	}
	return keys
}

func collectVectorEvidence(evidence map[string]float64, results []retrieval.VectorResult) {
	for _, result := range results {
		if result.Score > evidence[result.Key] {
			evidence[result.Key] = result.Score
		}
	}
}

func lexicalCoverage(query, text string) float64 {
	queryTerms := significantTokens(query)
	if len(queryTerms) == 0 {
		return 0
	}
	textTerms := make(map[string]struct{})
	for _, token := range retrieval.Tokenize(text) {
		textTerms[token] = struct{}{}
	}
	matched := 0
	for _, term := range queryTerms {
		if _, ok := textTerms[term]; ok {
			matched++
		}
	}
	return float64(matched) / float64(len(queryTerms))
}

func significantTokens(text string) []string {
	stop := map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "are": {}, "for": {}, "from": {}, "in": {}, "is": {}, "it": {}, "of": {},
		"on": {}, "or": {}, "the": {}, "to": {}, "tool": {}, "use": {}, "with": {}, "please": {}, "can": {}, "you": {},
		"find": {}, "get": {}, "show": {}, "me": {}, "my": {},
	}
	seen := map[string]struct{}{}
	var result []string
	for _, token := range retrieval.Tokenize(text) {
		if _, ignored := stop[token]; ignored {
			continue
		}
		if _, duplicate := seen[token]; duplicate {
			continue
		}
		seen[token] = struct{}{}
		result = append(result, token)
	}
	if len(result) != 0 {
		return result
	}
	for _, token := range retrieval.Tokenize(text) {
		if _, duplicate := seen[token]; duplicate {
			continue
		}
		seen[token] = struct{}{}
		result = append(result, token)
	}
	return result
}

func preferenceAdjustment(rules []routingprefs.Rule, item candidate, context string) float64 {
	for _, rule := range rules {
		if rule.Spec.Specificity == routingprefs.SpecificityConditionalTool && !conditionApplies(rule.Spec.Condition, context) {
			continue
		}
		strength := preferenceStrength(rule)
		for _, target := range rule.Spec.Preferred {
			if target.ServerID == item.source.ServerID && target.ToolName == item.source.ToolName {
				return strength
			}
		}
		for _, target := range rule.Spec.Deprioritized {
			if target.ServerID == item.source.ServerID && target.ToolName == item.source.ToolName {
				return -strength
			}
		}
	}
	return 0
}

func preferenceStrength(rule routingprefs.Rule) float64 {
	strength := maxPreferenceAdjustment / 2
	if rule.Spec.ProfileID != "" {
		strength = maxPreferenceAdjustment
	}
	switch rule.Spec.Specificity {
	case routingprefs.SpecificityConditionalTool:
		return strength
	case routingprefs.SpecificityToolSet:
		return strength * 0.75
	case routingprefs.SpecificityServer:
		return strength * 0.5
	default:
		return 0
	}
}

func conditionApplies(condition, context string) bool {
	condition = strings.TrimSpace(strings.ToLower(condition))
	if condition == "" {
		return true
	}
	context = strings.TrimSpace(strings.ToLower(context))
	if strings.Contains(context, condition) {
		return true
	}
	conditionTerms := significantTokens(condition)
	if len(conditionTerms) == 0 {
		return false
	}
	contextSet := map[string]struct{}{}
	for _, token := range significantTokens(context) {
		contextSet[token] = struct{}{}
	}
	matched := 0
	for _, token := range conditionTerms {
		if _, ok := contextSet[token]; ok {
			matched++
		}
	}
	return float64(matched)/float64(len(conditionTerms)) >= 0.75
}
