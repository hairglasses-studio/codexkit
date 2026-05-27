---
paths:
  - "baselineguard/**"
---

Baseline guard validation engine:
- Checks repos for canonical parity patterns (AGENTS.md, CLAUDE.md, .codex/, etc.)
- Returns structured Report with Finding entries — not simple pass/fail
- Zero external dependencies — stdlib only
- New check types must be registered in the check registry
- guard_test.go validates all check types with fixture repos
