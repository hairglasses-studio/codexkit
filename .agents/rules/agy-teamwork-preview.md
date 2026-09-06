# AGY 2.0 Teamwork Preview & Delegation Rules

Rules governing multi-agent teamwork previews (`teamwork-preview`), subagent delegation, and artifact lifecycle management across the AGY harness control plane.

## 1. Multi-Agent Teamwork Previews
- **Agent Scope & Write Boundaries**: Every delegated subagent MUST operate strictly within its assigned folder inside `.agents/` or designated file scope. Overlapping writes outside owned boundaries are prohibited.
- **Role Activation**: Subagent tasks MUST explicitly specify role assignments (e.g. `implementer`, `qa`, `specialist`). Primary execution follows the `implementer` role unless specified.
- **Parallel Dispatch**: Dispatch tasks in parallel to independent subagent instances whenever possible to minimize latency.

## 2. Subagent Delegation Workflow
1. **Prompt Crafting**: Formulate structured prompts specifying target working directory, dispatch path, ownership boundary, objectives, and acceptance criteria.
2. **Draft Recording**: Record prompt parameters and task specifications in the subagent's `DISPATCH.md`.
3. **Objective Verification**: Define clear, objective, and independently executable verification commands (`make test`, `make check`, self-tests).
4. **Integrity Mode**: Maintain real state and logic. Hardcoded outputs, dummy implementations, or fabricated verification logs are strictly forbidden.

## 3. Artifact Lifecycle Management
- **Directory Structure**: All agent metadata (plans, progress heartbeats, briefing files, handoffs) MUST be placed in `.agents/<agent_name>/`. Source code, tests, or runtime data must NEVER be placed in `.agents/`.
- **BRIEFING.md**: Every subagent MUST maintain `BRIEFING.md` as its working memory, preserving append-only identity and key constraint sections.
- **Progress Heartbeat**: `progress.md` MUST be updated after every meaningful step to serve as the liveness heartbeat.
- **Handoff Report**: Every completed task MUST produce a 5-component `handoff.md` (Observation, Logic Chain, Caveats, Conclusion, Verification Method).

## 4. Verification & Clean Handover
- Execute full build, test, lint, and convention checks before declaring task completion.
- Send handoff summary and `handoff.md` path to parent agent via `send_message`.
