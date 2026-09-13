# 01 Entry and prompting

Primary sources: OAI-CX-010, OAI-CX-011, OAI-CX-012.

## Verified facts

- Codex CLI is the terminal surface for code inspection, edits, command execution, and repeatable automation.
- The official quickstart separates general ChatGPT surfaces from developer-focused CLI and IDE entry points.
- API prompt-engineering guidance remains separate from Codex-specific entry documentation.

## Inferences for this harness

- Keep launcher and prompt-template guidance separate: CLI launch posture belongs in harness config, while task prompt quality belongs in reusable instruction or skill layers.
- Prompting guidance should cite API-level documents only when it changes how we ask models to reason, structure output, or preserve context.

## Volatile items

- CLI command flags and authentication flows.
- Model-specific prompting guidance.
- Surface naming and onboarding flow changes.

## Provider-surface availability

| Surface | Research stance |
| --- | --- |
| CLI | Canonical for terminal automation and local harness execution. |
| IDE | Entry context is editor-oriented; do not assume CLI flags apply. |
| Desktop app | User-facing command center; separate from CLI launch contracts. |
| Cloud | Repository/environment setup belongs in lane 06, not CLI setup. |
