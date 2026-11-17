# Operator Discipline & Multi-Agent Invariants

1. **Commit Attribution Invariant**:
   Preserve the repository's real author identity. Omit AI author/co-author
   credits and session attribution from commits and PRs, including
   `Co-Authored-By: Claude` and `Claude-Session:` trailers. Canonical rule:
   `claude-harness/claude/rules/no-ai-attribution.md` in the sibling harness.
   Record task ownership in the fleet coordination receipts instead.

2. **Commit Only What You Changed, From a Temporary Worktree, After Verifying**:
   Never commit from a primary checkout (the repo directory other sessions
   share). Make the change in a temporary worktree off local `main` —
   `git -C <repo> worktree add -b <lane> /tmp/<lane> main` or
   `cline --worktree` — commit there, land it, then remove the worktree.
   Run the repo's documented gate before committing and commit only when it
   passes; report the weakest state you actually established.
   Stage by explicit path. `git add -A`, `git add .`, and `git commit -a` are
   forbidden: they sweep another session's dirty files into your commit, which
   is exactly how that incident broke `jobb`'s `go.mod` and reverted ~100 files
   in `ralphglasses`. Never revert or delete changes you did not make, and never
   delete a `.git/index.lock` you did not create.

3. **Land Through a Locally Gated PR** (UPDATED 2026-10-26):
   Run `claude-harness/bin/hg-fast-gate --repo <abs path> --base origin/main
   --log LOG`, then `claude-harness/bin/hg-land --repo <abs path> --local-gate
   LOG` (branch -> PR -> squash merge). The log must end with `rc=0` and name
   the commit being landed. Never force-push or land a peer's unreviewed work.
   Require the `LANDED <repo> <merge-sha>` receipt and read back the remote
   main ref. Single source: `claude-harness/claude/rules/fleet-git-push-delegation.md`
   sections 000 and 00. Follow the session's active attribution requirements.

4. **Single-Owner Write Leases**:
   Before modifying a repository, claim write scope:
   `ralphglasses_session_message claim=true claim_resource=<repo>` or ensure
   `~/.local/state/fleet/claims/<repo>.json` exists with an ISO 8601 `claimed_at`.
   A claim exempts the primary checkout from the shared-tree rules for
   `CLINE_GIT_GUARD_CLAIM_TTL_HOURS` (default 12) and then expires; it never
   permits `git reset --hard` in a primary. Prefer `cline --worktree`
   so a task cannot commit out of a shared primary checkout at all.

5. **Zero Clipboard Invariant**:
   Never pollute the user's desktop Wayland/X11 clipboard (`wl-copy`, `xclip`).
   All inter-agent snippets and data buffers must route through `fleet-artifact-mcp` or `FLEET_INTER_SESSION_MAILBOX`.

6. **Hardware Floor Sentinels (HW-G01)**:
   - Root NVMe storage floor must remain >= 70.0 GB free.
   - GPU VRAM utilization must remain <= 12,000 MB.
   - Available RAM must remain >= 2.0 GB.

7. **Plateau Detection & Runaway Loop Defense**:
   Autonomous operations loops must assert positive finite ceilings (`--max-iterations > 0`).
   If 3 consecutive cycles yield identical diffs with no progress, the session must trip into `plateau_paused` rather than burning quota in an infinite loop.

8. **Structured Protocol v2.0 Fleet Messaging**:
   Use `fleet_bus.db` (`fleet_events`, `fleet_direct_messages`) and RFC 8785 canonical envelopes for inter-agent communication.
   Never inject unsolicited prompts into interactive terminal sessions (`tmux send-keys` is forbidden).

9. **Fleet-Terse Execution Discipline & Action-Density Standard (FLT-T01)**:
   In autonomous fleet operations, agents must maximize tool action density and minimize conversational prose overhead:
   - **Zero Preambles**: Emit tool calls immediately. Never output conversational intros ("I will now check...", "Let me run...") before emitting a tool call.
   - **Zero Raw Output Regurgitation**: Never echo large raw tool outputs or file contents into conversational prose.
   - **Telemetry is runtime-owned (rewritten 2026-10-26)**: the Cline team runtime already prints
     every iteration, tool start/finish and sampled mission-log line. Never type those lines in
     assistant text and never restate tool start/finish or iteration counts. For a milestone the
     coordinator must see (claimed, blocked, handoff, done), call `team_mission_log` with `kind`, a
     summary of at most 90 characters (the TUI cuts at 90) and `evidence` paths, in the same turn
     as a work call: a turn whose only call is `team_mission_log` or an empty mailbox read is waste
     (measured 2026-10-26: 11.9% of 15,176 teammate iterations were team-bookkeeping-only).
     The final report starts with the result, no tag. Read a team run with
     `cline-team-log-compact` (one line per run), not by scrolling the transcript.
   - **Bounded Payload Discipline**: Always specify bounded `limit` (<=50) or filter parameters when invoking ledger, session, or scan tools. Never fetch unbounded session collections over WebSocket/stdio channels.
   - **Async Specialist Delegation**: Decompose multi-host work into focused teammates via
     `team_spawn_teammate` + `team_run_task({agentId, task, taskId, runMode: "async"})`. The runtime
     executes at most 2 async runs at once (`maxConcurrentRuns` default 2; the rest queue) and one
     run per teammate, so dispatch at most 2 and always pass `taskId`. After dispatch, state `{agentId, runId}` once: `[team run] progress`
     lines name only the run id. Teammates inherit the lead's provider, model and effort at spawn.
   - **Await by runId (rewritten 2026-10-26)**: call `team_await_runs({runId})` for the run you
     dispatched. A bare `team_await_runs` awaits every run the team ever created and throws if any
     one of them failed, so one old failure breaks every later await-all. Poll `team_list_runs` for
     progress; do not trust a sync `team_run_task` status (upstream cline/cline#12062 reports
     "running" after completion). Await before sending a teammate another run. Dispatching to a teammate whose run is still active raises
     `SessionRuntime state is "running"; call canStartRun() first`, and that error path calls
     `failSession` — it destroys the whole session, it does not queue the input. After a session
     recovery, check `team_status` before respawning anything: a recovered session already carries
     its teammates, and respawning gave one 2026-09-18 session three copies of every teammate.

10. **Never `git stash` in a shared checkout**:
    Every primary checkout under `~/hairglasses-studio/<repo>` is shared with other live sessions.
    `git stash` there takes THEIR uncommitted work, and `git stash pop` reaches for whatever is on
    top of the stack. Measured 2026-09-18: a Cline session ran `stash && rebase && stash pop` in
    `secretstudios-mcp`, and the pop targeted a different session's day-old stash entry and failed.
    The same applies to `git rebase` in a dirty tree (autostash is a stash), `reset --hard`,
    `checkout`/`switch` of a branch, and `clean -f`. Use a temp worktree or `cline --worktree`.
    `cline/hooks/PreToolUse.py` enforces this; the rule is here because a blocked tool call should
    be a surprise, not your first hint.

11. **Post only as your own address**:
    Your fleet address is `cline:<session-id>` and TaskStart injects it at the top of the session.
    Never pass `--from <another agent>` to `hg-tier1` or any bridge tool. Measured 2026-09-18: a
    Cline session posted 1578 rows as `agy:hairglasses-studio-b6a2f7ed`, an address with no
    registry entry, which makes every claim and finding in that thread unattributable.

12. **Announce writes to shared files**:
    `~/.agents/memory/*`, fleet indexes, manifests and roadmaps are read by every session. Say on
    tier1 what you wrote, before or immediately after writing it.

13. **"Verified" requires quoted output**:
    Never write verified, confirmed, or passing without the command and its real output in the same
    report. A 2026-09-18 attestation claimed "fan-out verified across 121 active cluster members"
    when fan-out had no liveness filter at all, and shipped a `RawFetchVerified: true` constant that
    no fetch ever set. Report the weakest state you actually established: queued != delivered !=
    read != acked.
