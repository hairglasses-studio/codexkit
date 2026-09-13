# 10 Observability and evals

Primary sources: OAI-API-100, OAI-API-101, OAI-CX-102, OAI-CX-050.

## Verified facts

- OpenAI API eval workflow guidance is maintained in API documentation.
- Agents SDK observability belongs to the API orchestration surface.
- Developer settings are a client-facing surface separate from config-file reference.
- Hooks can support custom logging or validation at Codex lifecycle events.

## Inferences for this harness

- Codex harness observability should separate three concerns: local lifecycle hooks, client settings/logs, and API/Agents SDK traces or evals.
- Evals can validate prompt or orchestration behavior, but they do not prove local sandbox or approval behavior.
- Hook-based telemetry should be opt-in, reviewed, and bounded because hooks execute scripts.

## Volatile items

- Evals grader APIs and prompt optimizer behavior.
- Agents SDK tracing/export behavior.
- Developer logging and telemetry settings.
- Hook event payloads.

## Migration checks

- Do not mix API eval results with local permission proofs.
- Keep PII/secrets redaction policy attached to any logging hook.
- Recheck observability guidance before adopting a vendor/export integration.
