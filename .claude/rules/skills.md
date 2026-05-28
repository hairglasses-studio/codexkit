---
paths:
  - ".agents/skills/**"
---

Fleet skill distribution:
- Each canonical skill directory under `.agents/skills/` must contain a valid `SKILL.md` with portable frontmatter
- Changes propagate to generated `.claude/skills/` and plugin mirrors via `codexkit skills sync`
- Validate with `codexkit baseline check` before committing
- `.agents/skills/` is the canonical source; generated mirrors stay derived and should not become independent edit surfaces
- Keep skill descriptions focused: one workflow family per skill, thin routing not large manuals
