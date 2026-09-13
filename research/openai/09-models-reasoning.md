# 09 Models and reasoning

Primary sources: OAI-API-090, OAI-API-091, OAI-CX-092, OAI-CX-012.

## Verified facts

- The model catalog is the official current source for model families and availability.
- Reasoning behavior and reasoning controls are documented in API-level guidance.
- Codex feature and billing availability can depend on whether the user authenticates with ChatGPT or an API key.
- Prompting guidance is separate from model catalog data and should be refreshed when changing model-specific instructions.

## Inferences for this harness

- Do not freeze model choices in research prose. Put model pins in executable config templates and cite the catalog/reasoning docs for the policy basis.
- Treat reasoning effort as a model-supported capability, not a universal knob.
- Plan/price guidance is volatile and should be refreshed before any automation that could spend credits or tokens.

## Volatile items

- Model aliases and deprecations.
- Reasoning effort semantics and model-specific constraints.
- Plan limits, API pricing, and feature coverage by sign-in path.
- Prompting migrations for new model families.

## Migration checks

- Before changing model defaults, refresh OAI-API-090, OAI-API-091, OAI-CX-092, and OAI-CX-000.
- Record whether a model decision applies to CLI, SDK, IDE, cloud, or API-only work.
- Keep stale model pins out of project-local defaults unless a compatibility exception is documented.
