package ctx

// Merge combines two registries. override takes precedence over base.
// Returns a new Registry; neither input is modified.
func Merge(base, override *Registry) *Registry {
	out := NewRegistry()

	// Deep-copy base into out.
	for name, ss := range base.SkillSets {
		cp := *ss
		cp.Skills = append([]Skill(nil), ss.Skills...)
		cp.CompanionSets = append([]string(nil), ss.CompanionSets...)
		cp.DependsOn = append([]string(nil), ss.DependsOn...)
		out.SkillSets[name] = &cp
	}
	for name, sk := range base.Skills {
		cp := *sk
		cp.Tags = append([]string(nil), sk.Tags...)
		out.Skills[name] = &cp
	}
	for name, m := range base.MCPs {
		cp := *m
		cp.Args = append([]string(nil), m.Args...)
		cp.Env = copyMap(m.Env)
		out.MCPs[name] = &cp
	}
	for name, c := range base.CLIs {
		cp := *c
		cp.DefaultArgs = append([]string(nil), c.DefaultArgs...)
		out.CLIs[name] = &cp
	}

	// Apply override (overwrites by key).
	for name, ss := range override.SkillSets {
		cp := *ss
		cp.Skills = append([]Skill(nil), ss.Skills...)
		cp.CompanionSets = append([]string(nil), ss.CompanionSets...)
		cp.DependsOn = append([]string(nil), ss.DependsOn...)
		out.SkillSets[name] = &cp
	}
	for name, sk := range override.Skills {
		cp := *sk
		cp.Tags = append([]string(nil), sk.Tags...)
		out.Skills[name] = &cp
	}
	for name, m := range override.MCPs {
		cp := *m
		cp.Args = append([]string(nil), m.Args...)
		cp.Env = copyMap(m.Env)
		out.MCPs[name] = &cp
	}
	for name, c := range override.CLIs {
		cp := *c
		cp.DefaultArgs = append([]string(nil), c.DefaultArgs...)
		out.CLIs[name] = &cp
	}

	return out
}

// GetSkill returns a skill by name, checking SkillSets first, then LegacySkills.
func (r *Registry) GetSkill(name string) *Skill {
	for _, ss := range r.SkillSets {
		for _, s := range ss.Skills {
			if s.Name == name {
				return &s
			}
		}
	}
	return nil
}

// GetLegacySkill returns a legacy skill by name.
func (r *Registry) GetLegacySkill(name string) *LegacySkill {
	return r.Skills[name]
}

// Stats returns a summary of registry contents.
func (r *Registry) Stats() (skillSets, skills, mcps, clis int) {
	return len(r.SkillSets), len(r.Skills), len(r.MCPs), len(r.CLIs)
}

func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
