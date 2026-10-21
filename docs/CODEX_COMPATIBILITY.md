# Codex compatibility: reviewed 0.154.0 contract, installed 0.163.0

The installed CLI reported `codex-cli 0.163.0` on 2026-10-21. The feature and
protocol contract below was last reviewed against `0.154.0`; newer behavior
remains unverified until this document is re-audited. The Codex CLI changelog
lists 0.163.0 on 2026-10-15.

## Model routes and aliases

- `gpt-5.6-sol` is the flagship Codex model; the API alias is `gpt-5.6`. Supported reasoning effort levels: `low`, `medium` (default), `high`, `xhigh`, `max`.
- `gpt-5.6-terra` is the balanced route for broad scans and exploratory research. Supported reasoning effort levels: `low`, `medium` (default), `high`, `xhigh`, `max`, `ultra`.
- `gpt-5.6-luna` is the efficient route for narrow, low-cost execution. Supported reasoning effort levels: `low`, `medium` (default), `high`, `xhigh`, `max`.
- `gpt-5.5` is the base fast model. Supported reasoning effort levels: `low`, `medium` (default), `high`, `xhigh`.
- `gpt-6-astra` (Astra) is the next-generation high-depth reasoning model, targeted for complex architectural and multi-step tasks.
- `codex-auto-review` is the specialized model used for automatic approval review routing under `--approve-for-me`.

## Reasoning effort controls

Codex 0.154.0 provides granular control over model reasoning effort via configuration:

- `model_reasoning_effort`: Sets the default reasoning depth for turns. Supported levels depend on the model family:
  - Standard models (`gpt-5.6-sol`, `gpt-5.6-luna`, `gpt-5.5`): `low`, `medium`, `high`, `xhigh`, `max`.
  - Extended reasoning (`gpt-5.6-terra`): includes `ultra` for automatic task delegation.
  - Astra configurations (`gpt-6-astra`): recommend `model_reasoning_effort = "xhigh"` for deep problem decomposition and verification.
- `plan_mode_reasoning_effort`: Configures reasoning effort specifically when generating and updating execution plans.
- `hide_agent_reasoning` / `show_raw_agent_reasoning`: Boolean controls to govern whether reasoning traces are collapsed or displayed in the TUI.

Example profile configuration in `$CODEX_HOME/config.toml`:

```toml
[profiles.codex_harness_astra]
model = "gpt-6-astra"
model_reasoning_effort = "xhigh"
sandbox_mode = "danger-full-access"
approval_policy = "never"
```

## CLI flags and subcommands

Codex 0.154.0 provides rich non-interactive automation, debugging, and review subcommands:

### Core execution flags

- `-c, --config <key=value>`: Override configuration values from `$CODEX_HOME/config.toml` using dotted TOML paths (e.g. `-c model="gpt-5.6-sol"`).
- `--enable <FEATURE>` / `--disable <FEATURE>`: Repeatable feature flag toggles (e.g. `--enable code_mode`).
- `--strict-config`: Fail immediately if `config.toml` contains unrecognized keys.
- `-m, --model <MODEL>`: Set the model for the session.
- `-p, --profile <NAME>`: Layer `$CODEX_HOME/<name>.config.toml` or `[profiles.<name>]` over the base user configuration.
- `-s, --sandbox <MODE>`: Select command sandbox policy: `read-only`, `workspace-write`, or `danger-full-access`.
- `--approve-for-me`: Route approval requests through automatic review within the `workspace-write` sandbox.
- `--dangerously-bypass-approvals-and-sandbox`: Disable confirmation prompts and execute without sandboxing.
- `--dangerously-bypass-hook-trust`: Execute enabled hooks without requiring persisted trust prompts.
- `-C, --cd <DIR>`: Set the working directory root.
- `--worktree`: Run the session in a newly allocated Git worktree.
- `--add-dir <DIR>`: Grant write access to additional directories outside the primary workspace.
- `-a, --ask-for-approval <POLICY>`: Command approval policy (`on-request` or `never`).
- `--search`: Enable native live web search.
- `--no-alt-screen`: Run the TUI in inline scrollback mode.

### Subcommands

- `exec` (alias `e`): Non-interactive agent execution.
  - `--json`: Emit structured JSONL events to stdout.
  - `-o, --output-last-message <FILE>`: Save the final agent response to a dedicated file.
  - `--output-schema <FILE>`: Validate the final model response against a JSON Schema.
  - `--ephemeral`: Execute without persisting session rollout logs to disk.
  - `--skip-git-repo-check`: Allow execution in unversioned directories.
  - Subcommands: `resume` (continue session by ID or `--last`), `fork` (branch session), `review` (run code review).
- `review`: Run non-interactive code review against changes.
  - `--uncommitted`: Review staged, unstaged, and untracked changes in the working tree.
  - `--base <BRANCH>`: Review diff against a specified base branch.
  - `--commit <SHA>`: Review a specific commit.
  - `--title <TITLE>`: Custom title for review output.
- `debug`: Diagnostics and inspection tooling.
  - `debug prompt-input [OPTIONS] [PROMPT]`: Renders the complete, model-visible prompt input list as JSON. This exposes the concatenated system prompt, loaded `AGENTS.md` / `project-doc` blocks, environment context, and plugin recommendations. Essential for verifying instruction loading.
  - `debug models`: Outputs the raw model catalog JSON, including token limits, supported reasoning levels, and service tiers.
  - `debug app-server`: Utilities for inspecting and debugging the background app server daemon.
- `features`: Inspect and manage feature flags (`features list`, `features enable`, `features disable`).
  - `code_mode`: Supported in 0.154.0 with `code_mode_host` (stable), and `code_mode`, `code_mode_only`, `code_mode_prewarm`, `code_mode_interrupt` under active development.
- `doctor`: Inspects binary health, bundled search tools (`rg`), git integration, terminal capabilities, SQLite databases (`state`, `logs`, `goals`, `memories`, `thread_history`), rollout logs, auth status, sandbox state, and network reachability.
- Session lifecycle: `resume`, `fork`, `apply` (alias `a` for `git apply`), `queue`, `archive`, `unarchive`, and `delete`.

## Authentication modes

Codex 0.154.0 supports two distinct authentication pathways:

1. **ChatGPT OAuth / Prolite (`chatgpt`)**:
   - Authenticated via browser OAuth (`codex login`), device authorization (`codex login --device-auth`), or access token piped to stdin (`printenv CODEX_ACCESS_TOKEN | codex login --with-access-token`).
   - Managed credential storage: configured via `cli_auth_credentials_store = "file"` (`$CODEX_HOME/auth.json`) or OS keychain (`"keychain"`).
   - Backend wire protocol: WebSocket streaming via `wss://chatgpt.com/backend-api/...` with HTTP reachability checks.
   - Grants access to subscription plan quotas, ChatGPT Work workspace scopes (`forced_chatgpt_workspace_id`), cloud rollouts, and preview reasoning models.
2. **OpenAI Platform API Key (`api_key`)**:
   - Authenticated via `printenv OPENAI_API_KEY | codex login --with-api-key` or standard `OPENAI_API_KEY` environment variable.
   - Connects directly to OpenAI Platform API endpoints (`https://api.openai.com/v1` or custom `openai_base_url`).
   - Direct metered token billing; bypasses ChatGPT subscription rate limits.
   - Cloud task sync and ChatGPT Work collaboration features are disabled under API-key mode.

## Instruction loading budget and truncation traps

Codex builds an instruction chain on startup, traversing from the repository root (identified by `.git`) down to the current working directory:

- In each traversed directory, Codex loads at most one file: `AGENTS.override.md` > `AGENTS.md` > configured `project_doc_fallback_filenames`.
- Deeper directory instructions appear later in the prompt chain, allowing localized rules to override or specialize root guidance.
- Global instructions from `$CODEX_HOME` (`AGENTS.override.md` or `AGENTS.md`) are prepended to the prompt.

### Byte budget (`project_doc_max_bytes`)

- **Default limit**: `project_doc_max_bytes = 32768` (32 KiB).
- When cumulative project instruction files exceed `project_doc_max_bytes`, Codex stops adding subsequent files or truncates the prompt block with a warning.
- **The truncation trap**: Because project instructions are loaded top-down, a large root `AGENTS.md` can exhaust the byte budget early, silently discarding subtree-specific `AGENTS.md` instructions deeper in the working tree.
- **Mitigation & best practices**:
  1. Verify loaded prompt inputs using `codex debug prompt-input` to confirm that the entire `<INSTRUCTIONS>` block is intact.
  2. For repositories with extensive guidance, expand the budget in `$CODEX_HOME/config.toml`:
     ```toml
     project_doc_max_bytes = 65536
     ```
  3. Keep root `AGENTS.md` lean (<10–15 KiB), deferring specialized subsystem instructions to nested directories or on-demand references.

## Discovery, approvals, and MCP status

- `apps`, `goals`, `hooks`, `multi_agent`, `plugins`, `skill_search`, and `tool_suggest` are stable in Codex 0.154.0.
- Tool Search is available; its legacy feature toggle is a removed compatibility no-op. The client sets `defer_loading` on function namespaces or MCP server definitions.
- This server actively negotiates MCP `2025-11-25`. Codex's `mcp_2026_07_28` feature remains under development and disabled, so this repo does not claim that preview protocol is active.
- `tools/list` returns complete JSON Schema object inputs and supports cursor pagination. The `tool_catalog` and `tool_search` tools group compact entries by their registered module namespace; `tool_schema` loads one complete schema. Discovery filters accept no more than 64 `allowed_tools` entries and are not an authorization grant.
- Generated Codex MCP profiles bound their `enabled_tools` selection to 512 unique names (above the largest current curated fleet profile) and reject overlap with `disabled_tools`. MCP annotations and catalog `approval_hint` values remain advisory. The OpenAI client or operator owns the actual `require_approval`/approval policy, especially for remote or sensitive tools.

## Primary references

- [OpenAI latest-model guide](https://developers.openai.com/api/docs/guides/latest-model)
- [OpenAI Tool Search guide](https://developers.openai.com/api/docs/guides/tools-tool-search)
- [OpenAI MCP and connectors guide](https://developers.openai.com/api/docs/guides/tools-connectors-mcp)
- [Codex CLI changelog](https://learn.chatgpt.com/docs/changelog#month-2026-10)
- [Codex 0.154.0 release](https://github.com/openai/codex/releases/tag/rust-v0.154.0)
- [Codex 0.154.0 feature registry](https://github.com/openai/codex/blob/rust-v0.154.0/codex-rs/features/src/lib.rs)
- [MCP Go SDK v1.7.0 release](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0)
- [MCP Go SDK v1.7.0 server implementation](https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/mcp/server.go)

## Codex harness lifecycle platform boundary

The durable harness lifecycle engine is available on Linux and macOS. Both
backends require an owned target root, no-follow directory traversal, a private
state store, same-directory atomic stage commits, per-object ownership ledger
checks, post-commit read-back, append-only receipts, and retained quarantine
objects for rollback. Darwin uses `openat`/`renameatx_np` with exclusive or swap
semantics; it fails closed for symlink leaves because the current macOS backend
does not establish the descriptor-pinned symlink contract. Existing regular
files that cannot be opened for descriptor pinning are also refused without
mutation. Unsupported platforms continue to return `ErrUnsupported`. This
describes source support only; it is not a runtime qualification claim for a
particular macOS release or filesystem.
