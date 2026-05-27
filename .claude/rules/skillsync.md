---
paths:
  - "skillsync/**"
---

Skill sync engine:
- Syncs `.agents/skills/surface.yaml` to `.claude/skills/` and plugin mirrors
- Non-destructive by default — supports dry-run via Diff()
- `surface.yaml` is the authoritative source list — never hand-edit generated mirrors
- Sync() is append-only in the Go implementation (differs from shell script which replaces)
- Uses only portable frontmatter keys: name, description, allowed-tools
