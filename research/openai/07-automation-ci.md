# 07 Automation and CI

Primary sources: OAI-CX-070, OAI-CX-071, OAI-CX-072.

## Verified facts

- Non-interactive mode uses `codex exec` to run from scripts and CI without opening the interactive TUI.
- Codex GitHub Action guidance covers workflow-file automation for repeatable tasks such as review or release preparation.
- Codex Security CLI has CI-oriented scanning guidance separate from the hosted security plugin.

## Inferences for this harness

- CI recipes must avoid TUI-only assumptions and should record expected exit-code behavior for each automation.
- GitHub Action usage is a separate provider surface from locally launched `codex exec`; keep workflow permissions and runner assumptions explicit.
- Security scanning in CI should be version-pinned or version-checked against the security changelog before relying on new features.

## Volatile items

- `codex exec` flags and exit-code semantics.
- GitHub Action inputs and permissions.
- CI authentication requirements.
- Security scan SARIF behavior and beta-access requirements.

## Migration checks

- Validate noninteractive commands under stdin-closed CI conditions.
- Keep API-key and ChatGPT-authenticated automation paths separate.
- Recheck the changelog before enabling a new CI behavior as a gate.
