# 02 Config and policy

Primary sources: OAI-CX-020, OAI-CX-021, OAI-CX-022, OAI-CX-023, OAI-CX-024.

## Verified facts

- Codex reads user-level config and trusted project-scoped `.codex/config.toml` files.
- Current documented precedence is: CLI flags and `--config`, then trusted project config from root to current directory, then selected profile files, then user config, then system config, then built-in defaults.
- Project-local `.codex/` layers are skipped when a project is untrusted.
- Some project-local keys are ignored because they would redirect credentials, provider auth, notification, telemetry, or profile selection.
- The official configuration reference defines app and MCP tool approval modes
  as `auto`, `prompt`, `writes`, or `approve`; this enum is separate from the
  top-level command `approval_policy`, whose values include `never`.
- Managed configuration constrains supported local runtime behavior but does not replace workspace membership, seats, roles, or repository authorization.

## Inferences for this harness

- Treat config precedence and project trust as harness-critical. Any rendered template or migration must name the layer it targets.
- Keep provider/auth and telemetry settings in user or managed layers unless official docs explicitly allow a project-local override.
- Store local policy probes as tests against rendered config, not as prose-only conventions.

## Volatile items

- Config keys and ignored-key list.
- Profile-file behavior.
- `requirements.toml` enforcement options.
- Default model, web-search, approval, and sandbox defaults.

## Migration checks

- Verify generated config parses.
- Verify project-local config does not contain ignored provider/auth/profile/telemetry keys.
- Verify trusted-project assumptions are explicit in docs and tests.
- Verify managed requirements do not conflict with local templates.
