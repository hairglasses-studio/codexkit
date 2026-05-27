---
paths:
  - "skills/**"
---

Fleet skill distribution:
- Each skill directory must contain a valid SKILL.md with portable frontmatter
- Changes here propagate to ALL managed repos via `codexkit skills sync`
- Validate with `codexkit baseline check` before committing
- Skills are the canonical source — they get copied into repos' `.claude/skills/` and `.agents/skills/`
- Keep skill descriptions focused: one workflow family per skill, thin routing not large manuals
