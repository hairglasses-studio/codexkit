# 03 Instructions and skills

Primary sources: OAI-CX-030, OAI-CX-031, OAI-CX-032, OAI-CX-033.

## Verified facts

- Codex builds an instruction chain once per run or launched TUI session.
- Global guidance comes from Codex home, using `AGENTS.override.md` if present, otherwise `AGENTS.md`.
- Project guidance starts at the project root and walks down to the working directory.
- In each project directory Codex includes at most one instruction file, preferring `AGENTS.override.md`, then `AGENTS.md`, then configured fallback filenames.
- Later project files appear after earlier ones, so closer files can override broader guidance.
- The combined project instruction chain is bounded by `project_doc_max_bytes`, which the 2026-08-09 official snapshot documents as 32 KiB by default.
- Subagent workflows can spawn specialized agents in parallel and aggregate their results.
- Skills package instructions, resources, and optional scripts for repeatable workflows.
- Skills use progressive disclosure: selection can be explicit or implicit, but the selected `SKILL.md` remains the authoritative entrypoint for the workflow.
- Skill packages can include `agents/openai.yaml` metadata such as invocation policy and tool dependencies; disabling implicit invocation still leaves explicit skill calls available.

## Inferences for this harness

- Harness-owned global guidance should avoid duplicating repo-local rules; repo-local files should win by proximity.
- Generated compatibility mirrors should be tested for behavioral equivalence, not textual identity.
- Subagent and skill guidance should remain opt-in per task or repo policy; generic harness defaults should not silently authorize parallel writes.

## Volatile items

- Instruction byte limits and fallback filename handling.
- Subagent availability and custom-agent config schema.
- Skill package format and installation approval behavior.
- Skill invocation policy and bundled dependency metadata.
- Rules schema and trust behavior.

## Provider-surface availability

| Capability | Documented surface posture |
| --- | --- |
| AGENTS.md | Codex instruction hierarchy across local/project contexts. |
| Subagents | ChatGPT Work and Codex workflows; local Codex can define custom agents. |
| Skills | Shared ChatGPT and Codex extension mechanism. |
| Rules | Codex agent-configuration surface; track separately from instructions. |
