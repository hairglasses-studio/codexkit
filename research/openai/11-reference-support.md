# 11 Reference and support

Primary sources: OAI-CX-110, OAI-CX-111, OAI-CX-112.

## Verified facts

- The glossary defines Codex terminology across app, CLI, IDE, cloud, SDK, and related integrations.
- Troubleshooting docs are support references and should not be treated as normative defaults.
- The Codex Security plugin changelog records plugin-specific version availability; plugin, app, CLI, TypeScript SDK, and plugin app versions are separate.
- As of this 2026-08-09 snapshot, OAI-CX-112 documents the hosted Codex Security plugin catalog latest release as 0.1.17.

## Inferences for this harness

- Use the glossary to normalize naming before writing templates or migration records.
- Use troubleshooting for incident triage, not as the source of policy.
- Version-check the security plugin before relying on a new feature in a long-running scan or CI gate.

## Volatile items

- Security plugin catalog version.
- CLI marketplace version.
- Troubleshooting commands and known issue patterns.
- Glossary names for newly added surfaces.

## Refresh procedure

1. Load `sources.json`.
2. Check every `critical` source first.
3. Check source URLs syntactically and, when possible, with HTTP status.
4. Compare new official pages to the previous summary at the level of behavior: changed defaults, new surfaces, removed flags, new safety requirements, or new maturity labels.
5. Update only the affected lane files and leave unrelated local dirt untouched.
6. Run JSON validation and URL syntax validation.
7. Stage explicit `research/openai/**` files only.

## Support boundary

If an official page does not establish a behavior, mark the statement as an inference or unresolved item. Do not fill gaps with blog posts, third-party tutorials, repository issue comments, or stale local memory.
