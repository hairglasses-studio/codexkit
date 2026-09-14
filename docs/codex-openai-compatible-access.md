# Codex OpenAI-Compatible & Non-OAuth Access Configuration Guide

This document describes the configuration, setup, and verification of non-OAuth, API key-based access for OpenAI and OpenAI-compatible alternatives (e.g., OpenRouter, DeepSeek, vLLM, local LLM proxies) in OpenAI Codex CLI and `codexkit`.

---

## 1. Overview & Architecture

Codex supports two primary authentication and model dispatch paths:

1. **ChatGPT OAuth Mode** (`preferred_auth_method = "chatgpt"`):
   Uses OAuth session tokens stored in `~/.codex/auth.json` bound to a ChatGPT subscription account.
2. **API Key / OpenAI-Compatible Direct Mode** (`preferred_auth_method = "api_key"` or custom `model_providers`):
   Uses standard API keys (`OPENAI_API_KEY`, `OPENROUTER_API_KEY`, `DEEPSEEK_API_KEY`, etc.) and direct HTTP endpoints (`/v1/models`, `/v1/responses`) without requiring web-based OAuth browser login.

---

## 2. Configuration Files

Codex configuration follows a layered resolution hierarchy:

| Scope | Location | Description |
|-------|----------|-------------|
| **Global User Config** | `~/.codex/config.toml` | User-wide profiles, default model, and provider endpoints |
| **Workspace Root** | `<workspace-root>/.codex/config.toml` | Org-wide policy, MCP server declarations, and permissions |
| **Project / Repo** | `<repo>/.codex/config.toml` | Repo-scoped overrides and repo-specific MCP servers |
| **Credentials Store** | `~/.codex/auth.json` | Local credential store (`auth_mode`, API keys, session tokens) |
| **Fleet Management** | `codexkit/` | Go toolkit (`baselineguard`, `configindex`) validating configuration state |

---

## 3. Configuring OpenAI-Compatible Access

### 3.1 Model Provider Declarations (`~/.codex/config.toml`)

Custom endpoints and non-OAuth API providers are declared in `[model_providers.<id>]` tables in `~/.codex/config.toml`:

```toml
[model_providers.openai_direct]
name = "OpenAI Direct API"
base_url = "https://api.openai.com/v1"
env_key = "OPENAI_API_KEY"
requires_openai_auth = false
wire_api = "responses"

[model_providers.openrouter]
name = "OpenRouter"
base_url = "https://openrouter.ai/api/v1"
env_key = "OPENROUTER_API_KEY"
requires_openai_auth = false
wire_api = "responses"

[model_providers.deepseek]
name = "DeepSeek"
base_url = "https://api.deepseek.com/v1"
env_key = "DEEPSEEK_API_KEY"
requires_openai_auth = false
wire_api = "responses"

[model_providers.gemini]
name = "Google Gemini API"
base_url = "https://generativelanguage.googleapis.com/v1beta/openai"
env_key = "GEMINI_API_KEY"
requires_openai_auth = false
wire_api = "responses"

[model_providers.custom_local]
name = "Custom Local / Proxy"
base_url = "http://127.0.0.1:8000/v1"
env_key = "OPENAI_API_KEY"
requires_openai_auth = false
wire_api = "responses"
```

### 3.2 Field Definitions

- **`name`**: Descriptive label shown in CLI logs and status outputs.
- **`base_url`**: Base HTTP URL for the OpenAI-compatible v1 API endpoint.
- **`env_key`**: Environment variable containing the bearer API key.
- **`requires_openai_auth`**: Set to `false` for standard API key / non-OAuth endpoints.
- **`wire_api`**: Must be set to `"responses"` (Responses API protocol).

---

## 4. Multi-Provider Fallback Cascade & Reasoning Effort Profiles

### 4.1 Cascade Order Architecture

To maintain high availability and operational resilience, the studio terminal multiplexer (`scripts/cline-terminal-mux.sh`) and Codex CLI support an ordered multi-provider fallback cascade:

```
1. OpenAI Direct (api.openai.com/v1)  -> Primary sovereign endpoint
2. OpenRouter (openrouter.ai/api/v1)   -> High-throughput multi-model gateway
3. DeepSeek (api.deepseek.com/v1)      -> High-reasoning cost-efficient fallback
4. Gemini (generativelanguage.googleapis.com/v1beta/openai) -> High-context backup
```

Multiplexer sessions automatically probe and export the cascade configuration:
```bash
export CODEX_PROVIDER_CASCADE="openai_direct,openrouter,deepseek,gemini"
export CODEX_ACTIVE_PROVIDER="${ACTIVE_PRIMARY_PROVIDER:-openai_direct}"
```

### 4.2 Reasoning Effort Selection Profile (`gpt-5.6-sol`)

For the standard model `gpt-5.6-sol`, execution posture is controlled via reasoning effort profiles in `~/.codex/<profile>.config.toml`:

| Task Category | Profile Name | Target Model | Reasoning Effort | Sandbox / Approval |
|---------------|--------------|--------------|------------------|--------------------|
| **Planning & Strategy** | `planning` | `gpt-5.6-sol` | `high` | `danger-full-access` / `never` |
| **Code & Arch Audit** | `audit` | `gpt-5.6-sol` | `high` | `danger-full-access` / `never` |
| **Routine Operations** | `routine` | `gpt-5.6-sol` | `medium` | `danger-full-access` / `never` |
| **High Reasoning Override** | `sol_high` | `gpt-5.6-sol` | `high` | `danger-full-access` / `never` |
| **Balanced Speed Override** | `sol_medium` | `gpt-5.6-sol` | `medium` | `danger-full-access` / `never` |

Profiles are stored as standalone configuration overlays in `~/.codex/`:
- `~/.codex/openai_direct.config.toml`
- `~/.codex/openrouter.config.toml`
- `~/.codex/deepseek.config.toml`
- `~/.codex/gemini.config.toml`
- `~/.codex/planning.config.toml`
- `~/.codex/audit.config.toml`
- `~/.codex/routine.config.toml`

Named profiles allow fast provider and posture switching via `codex -p <profile>` or `codex exec -p <profile>`:

```toml
# Example ~/.codex/planning.config.toml
approval_policy = "never"
sandbox_mode = "danger-full-access"
model = "gpt-5.6-sol"
model_reasoning_effort = "high"
```

```toml
# Example ~/.codex/routine.config.toml
approval_policy = "never"
sandbox_mode = "danger-full-access"
model = "gpt-5.6-sol"
model_reasoning_effort = "medium"
```

---

## 5. Non-Interactive CLI Login & Overrides

### 5.1 Storing an API Key Non-Interactively

To seed the API key non-interactively without interactive prompts:

```bash
printenv OPENAI_API_KEY | codex login --with-api-key
```

### 5.2 Dynamic Command-Line Configuration

To run one-off tasks against an OpenAI-compatible endpoint using `codex exec`:

```bash
OPENAI_API_KEY="sk-proj-your-key" codex exec \
  -c model_providers.openai_direct.name="OpenAI Direct API" \
  -c model_providers.openai_direct.base_url="https://api.openai.com/v1" \
  -c model_providers.openai_direct.env_key="OPENAI_API_KEY" \
  -c model_providers.openai_direct.requires_openai_auth=false \
  -c model_providers.openai_direct.wire_api="responses" \
  -c model_provider="openai_direct" \
  -m gpt-4o \
  --skip-git-repo-check \
  "write a quick status report"
```

---

## 6. Verification & Testing

Non-interactive execution under API key mode was verified using an end-to-end mock server harness:

1. **Test Setup**:
   A local HTTP mock server listened on `127.0.0.1:18892` handling `GET /v1/models` and streaming `POST /v1/responses` events.
2. **Execution**:
   `codex exec` was dispatched with `OPENAI_API_KEY="sk-mock-key-verified"`, `model_provider="mock_provider"`, `requires_openai_auth=false`, and `wire_api="responses"`.
3. **Observed Results**:
   - Received `GET /v1/models?client_version=0.153.4` with `Authorization: Bearer sk-mock-key-verified...`.
   - Received `POST /v1/responses` with `Authorization: Bearer sk-mock-key-verified...`.
   - Session completed non-interactively with `Exit code: 0` without opening a browser or requesting OAuth login.

---

## 7. Fleet Validation & Maintenance

To ensure configuration files remain valid across the fleet:

```bash
cd codexkit
GOWORK=off go run ./cmd/codexkit baseline check .
```

All 29 baseline checks pass with the configured model providers and profiles.
