# Provider-parity fixtures

Ownership: `codex-harness-specific` for Codex parser/adapter fixtures.

These fixtures do not make provider-neutral policy canonical here. They prove
that the Codex-native projection preserves the intended safety behavior without
copying Claude or AGY hook schemas.

- `hooks/allow_echo.json` — positive control: safe read-only shell command
  produces no Codex hook decision.
- `hooks/deny_rm_rf_root.json` — negative control: broad destructive command
  emits a Codex `PreToolUse` deny decision.
- `hooks/deny_runtime_state_patch.json` — negative control: patching Codex
  runtime/auth/history state emits a Codex `PreToolUse` deny decision.
- `hooks/deny_permission_request_runtime_copy.json` — negative control:
  approval request for copying Codex runtime state emits a Codex
  `PermissionRequest` deny decision.
