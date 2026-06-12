package ctx

// Skill is one individual skill — model sees only name + 1-line description
// in context. Full Body is loaded only when ctx_skill_get is called.
type Skill struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Body        string   `json:"body"`
	Tags        []string `json:"tags,omitempty"`
}

// SkillSet is a curated JSON bundle of skills loaded from
// .ctx/skillsets/<name>/skillset.json.
type SkillSet struct {
	Schema    string   `json:"$schema,omitempty"`
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Skills    []Skill  `json:"skills"`
	Tags      []string `json:"tags,omitempty"`

	// CompanionSets are skill set names auto-suggested after loading any
	// skill from this set (displayed as 1-liners, zero bloat).
	CompanionSets []string `json:"companion_skillsets,omitempty"`
	DependsOn     []string `json:"depends_on,omitempty"`
}

// LegacySkill is a single .ctx/skills/<name>/SKILL.md file with YAML
// frontmatter. Loaded alongside SkillSets for backward compatibility.
type LegacySkill struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Body        string   `json:"body"`
	Tags        []string `json:"tags,omitempty"`
}

// MCPDefinition describes an MCP server from .ctx/mcps/registry.json.
type MCPDefinition struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env,omitempty"`
}

// CLIDefinition describes a CLI tool from .ctx/clis/registry.json.
type CLIDefinition struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Binary      string   `json:"binary"`
	DefaultArgs []string `json:"defaultArgs,omitempty"`
}

// Registry holds all loaded ctx artifacts. A merged view is produced by
// loading global (~/.ctx) first, then local (./.ctx) with deep-merge.
type Registry struct {
	SkillSets map[string]*SkillSet       `json:"skillsets"`
	Skills    map[string]*LegacySkill    `json:"skills"`
	MCPs      map[string]*MCPDefinition  `json:"mcps"`
	CLIs      map[string]*CLIDefinition  `json:"clis"`
}

// NewRegistry returns an empty, initialised Registry.
func NewRegistry() *Registry {
	return &Registry{
		SkillSets: make(map[string]*SkillSet),
		Skills:    make(map[string]*LegacySkill),
		MCPs:      make(map[string]*MCPDefinition),
		CLIs:      make(map[string]*CLIDefinition),
	}
}

// SkillSuggestion is one item returned by Suggest — just name + description.
// No body; that stays out of context until ctx_skill_get is called.
type SkillSuggestion struct {
	SkillSet      string   `json:"skillset,omitempty"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Tags          []string `json:"tags,omitempty"`
	CompanionSets []string `json:"companion_skillsets,omitempty"`
}

// Suggestions is the full response from Suggest().
type Suggestions struct {
	Skills []SkillSuggestion `json:"skills"`
	MCPs   []string          `json:"mcps,omitempty"`
	CLIs   []string          `json:"clis,omitempty"`
}
