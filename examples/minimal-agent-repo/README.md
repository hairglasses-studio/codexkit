# Minimal Agent Repo Fixture

This synthetic repo lets reviewers run codexkit dry-run commands without a
private workspace:

```bash
GOWORK=off go run ./cmd/codexkit skills diff examples/minimal-agent-repo
GOWORK=off go run ./cmd/codexkit mcp diff examples/minimal-agent-repo
```
