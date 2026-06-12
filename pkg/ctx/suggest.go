package ctx

import (
	"sort"
	"strings"
	"unicode"
)

// Suggest performs simple keyword-matching to find relevant skills, MCPs,
// and CLIs for the given task. Only name + 1-line description is returned
// per skill — zero token bloat until ctx_skill_get is called.
func (r *Registry) Suggest(task string, maxResults int) *Suggestions {
	if maxResults <= 0 {
		maxResults = 8
	}

	keywords := tokenize(task)
	if len(keywords) == 0 {
		return &Suggestions{}
	}

	// Score and collect skill suggestions.
	type scored struct {
		s   SkillSuggestion
		scr int
	}
	var scoredSkills []scored

	for _, ss := range r.SkillSets {
		for _, skill := range ss.Skills {
			scr := scoreSkill(keywords, skill.Name, skill.Description, skill.Tags)
			if scr <= 0 {
				continue
			}
			scoredSkills = append(scoredSkills, scored{
				s: SkillSuggestion{
					SkillSet:      ss.Name,
					Name:          skill.Name,
					Description:   skill.Description,
					Tags:          skill.Tags,
					CompanionSets: ss.CompanionSets,
				},
				scr: scr,
			})
		}
	}

	for _, skill := range r.Skills {
		scr := scoreSkill(keywords, skill.Name, skill.Description, skill.Tags)
		if scr <= 0 {
			continue
		}
		scoredSkills = append(scoredSkills, scored{
			s: SkillSuggestion{
				Name:        skill.Name,
				Description: skill.Description,
				Tags:        skill.Tags,
			},
			scr: scr,
		})
	}

	// Sort by score descending.
	sort.Slice(scoredSkills, func(i, j int) bool {
		return scoredSkills[i].scr > scoredSkills[j].scr
	})

	// Collect MCP and CLI matches.
	var mcpNames []string
	for name, mcp := range r.MCPs {
		if scoreGeneric(keywords, name, mcp.Description) > 0 {
			mcpNames = append(mcpNames, name)
		}
	}

	var cliNames []string
	for name, cli := range r.CLIs {
		if scoreGeneric(keywords, name, cli.Description) > 0 {
			cliNames = append(cliNames, name)
		}
	}

	sug := &Suggestions{
		MCPs: mcpNames,
		CLIs: cliNames,
	}

	n := maxResults
	if len(scoredSkills) < n {
		n = len(scoredSkills)
	}
	sug.Skills = make([]SkillSuggestion, n)
	for i := 0; i < n; i++ {
		sug.Skills[i] = scoredSkills[i].s
	}

	return sug
}

// scoreSkill returns a relevance score for a skill against the keyword set.
// Matches in name are weighted 3x, description 2x, tags 1x.
func scoreSkill(keywords map[string]bool, name, desc string, tags []string) int {
	nameTokens := tokenSet(name)
	descTokens := tokenSet(desc)
	tagTokens := tokenSet(strings.Join(tags, " "))

	score := 0
	for kw := range keywords {
		if nameTokens[kw] {
			score += 3
		}
		if descTokens[kw] {
			score += 2
		}
		if tagTokens[kw] {
			score += 1
		}
	}
	return score
}

// scoreGeneric returns > 0 if any keyword matches the name or description.
func scoreGeneric(keywords map[string]bool, name, desc string) int {
	nameTokens := tokenSet(name)
	descTokens := tokenSet(desc)
	for kw := range keywords {
		if nameTokens[kw] || descTokens[kw] {
			return 1
		}
	}
	return 0
}

// tokenize lowercases, splits on non-alpha, and deduplicates.
func tokenize(s string) map[string]bool {
	tokens := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		if len(t) < 2 || isStopWord(t) {
			continue
		}
		out[t] = true
	}
	return out
}

// tokenSet returns a set of tokens from a string (for fast lookup).
func tokenSet(s string) map[string]bool {
	tokens := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		if len(t) >= 2 {
			out[t] = true
		}
	}
	return out
}

// isStopWord returns true for common English stop words.
func isStopWord(w string) bool {
	switch w {
	case "the", "a", "an", "is", "are", "was", "were", "be", "been",
		"being", "have", "has", "had", "do", "does", "did", "will",
		"would", "could", "should", "may", "might", "shall", "can",
		"to", "of", "in", "for", "on", "with", "at", "by", "from",
		"as", "into", "through", "during", "before", "after", "above",
		"below", "between", "out", "off", "over", "under", "again",
		"further", "then", "once", "here", "there", "when", "where",
		"why", "how", "all", "each", "every", "both", "few", "more",
		"most", "other", "some", "such", "no", "nor", "not", "only",
		"own", "same", "so", "than", "too", "very", "just", "it", "its",
		"and", "but", "or", "if", "because", "about", "up", "this",
		"that", "these", "those", "which", "who", "whom", "what",
		"i", "me", "my", "we", "our", "you", "your", "he", "him", "his",
		"she", "her", "they", "them", "their", "us",
		"doing", "get", "got", "getting", "make", "made",
		"making", "use", "used", "using", "like", "want", "need",
		"try", "trying", "help", "set", "run", "go", "see", "know",
		"take", "think", "come", "look", "find", "give", "tell",
		"work", "call", "ask", "put", "new", "old", "good", "bad",
		"big", "small", "high", "low", "long", "short", "first",
		"last", "next", "able", "best", "better", "done", "sure":
		return true
	}
	return false
}
