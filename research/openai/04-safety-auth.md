# 04 Safety and authentication

Primary sources: OAI-CX-040, OAI-CX-041, OAI-CX-042, OAI-CX-043, OAI-CX-044.

## Verified facts

- Local desktop app, CLI, and IDE extension support ChatGPT sign-in and API-key sign-in for OpenAI models.
- Codex cloud requires ChatGPT sign-in.
- Approvals and sandboxing are distinct controls: approvals decide when Codex pauses, while sandboxing constrains command access.
- Auto-review changes who reviews eligible sandbox-boundary requests; it does not expand sandbox permissions.
- Permission profiles provide reusable filesystem and network policies.

## Inferences for this harness

- Safety defaults must be represented as behavior, not just UI wording. The harness should verify sandbox mode, writable roots, network access, and approval policy separately.
- API-key automation should be documented as a different access and billing path from ChatGPT-authenticated local work.
- Auto-review should be modeled as a review routing option, not a permission grant.

## Volatile items

- Approval policy semantics and failure modes.
- Protected path and writable-root behavior.
- Network access defaults.
- Credential storage and workspace-access policies.
- Built-in permission profiles.

## Migration checks

- Check every launcher or profile for sandbox, approval, and network intent.
- Confirm cloud assumptions use ChatGPT-authenticated workspace controls.
- Confirm API-key CI paths do not assume cloud-only features.
