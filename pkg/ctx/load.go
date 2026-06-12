package ctx

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load reads ctxDir (typically ~/.ctx) and returns a populated Registry.
// If ctxDir is empty, Load returns an empty Registry.
// Call Merge to combine global and local registries.
func Load(ctxDir string) (*Registry, error) {
	if ctxDir == "" {
		return NewRegistry(), nil
	}

	info, err := os.Stat(ctxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return NewRegistry(), nil
		}
		return nil, fmt.Errorf("stat ctx dir %s: %w", ctxDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("ctx path %s is not a directory", ctxDir)
	}

	r := NewRegistry()

	if err := loadSkillSets(ctxDir, r); err != nil {
		return nil, err
	}
	if err := loadLegacySkills(ctxDir, r); err != nil {
		return nil, err
	}
	if err := loadMCPs(ctxDir, r); err != nil {
		return nil, err
	}
	if err := loadCLIs(ctxDir, r); err != nil {
		return nil, err
	}

	return r, nil
}

// loadSkillSets scans .ctx/skillsets/*/skillset.json.
func loadSkillSets(ctxDir string, r *Registry) error {
	pattern := filepath.Join(ctxDir, "skillsets", "*", "skillset.json")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("glob skillsets: %w", err)
	}
	for _, path := range matches {
		ss, err := readSkillSet(path)
		if err != nil {
			return fmt.Errorf("read skillset %s: %w", path, err)
		}
		if ss.Name == "" {
			continue
		}
		r.SkillSets[ss.Name] = ss
	}
	return nil
}

func readSkillSet(path string) (*SkillSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ss SkillSet
	if err := json.Unmarshal(data, &ss); err != nil {
		return nil, err
	}
	return &ss, nil
}

// loadLegacySkills scans .ctx/skills/*/SKILL.md and parses YAML frontmatter.
func loadLegacySkills(ctxDir string, r *Registry) error {
	pattern := filepath.Join(ctxDir, "skills", "*", "SKILL.md")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("glob skills: %w", err)
	}
	for _, path := range matches {
		skill, err := readLegacySkill(path)
		if err != nil {
			return fmt.Errorf("read skill %s: %w", path, err)
		}
		if skill.Name == "" {
			continue
		}
		r.Skills[skill.Name] = skill
	}
	return nil
}

// frontmatter represents the YAML frontmatter in a SKILL.md file.
type frontmatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Tags        []string `yaml:"tags"`
	Metadata    struct {
		Hermes *struct {
			Tags []string `yaml:"tags"`
		} `yaml:"hermes"`
	} `yaml:"metadata"`
}

func readLegacySkill(path string) (*LegacySkill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)

	// Parse YAML frontmatter (delimited by ---).
	var fm frontmatter
	body := strings.TrimSpace(content)

	if strings.HasPrefix(body, "---") {
		rest := body[3:]
		idx := strings.Index(rest, "\n---")
		if idx > 0 {
			yamlBlock := rest[:idx]
			body = strings.TrimSpace(rest[idx+4:])
			if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
				// Non-fatal: some SKILL.md files may not have valid frontmatter.
				fm = frontmatter{}
			}
		}
	}

	tags := fm.Tags
	if fm.Metadata.Hermes != nil && len(fm.Metadata.Hermes.Tags) > 0 {
		tags = append(tags, fm.Metadata.Hermes.Tags...)
	}

	return &LegacySkill{
		Name:        fm.Name,
		Description: fm.Description,
		Body:        body,
		Tags:        tags,
	}, nil
}

// loadMCPs reads .ctx/mcps/registry.json.
func loadMCPs(ctxDir string, r *Registry) error {
	path := filepath.Join(ctxDir, "mcps", "registry.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read mcps registry: %w", err)
	}
	var mcpMap map[string]*MCPDefinition
	if err := json.Unmarshal(data, &mcpMap); err != nil {
		return fmt.Errorf("parse mcps registry: %w", err)
	}
	for name, mcp := range mcpMap {
		if mcp.Name == "" {
			mcp.Name = name
		}
		r.MCPs[name] = mcp
	}
	return nil
}

// loadCLIs reads .ctx/clis/registry.json.
func loadCLIs(ctxDir string, r *Registry) error {
	path := filepath.Join(ctxDir, "clis", "registry.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read clis registry: %w", err)
	}
	var cliMap map[string]*CLIDefinition
	if err := json.Unmarshal(data, &cliMap); err != nil {
		return fmt.Errorf("parse clis registry: %w", err)
	}
	for name, cli := range cliMap {
		if cli.Name == "" {
			cli.Name = name
		}
		r.CLIs[name] = cli
	}
	return nil
}
