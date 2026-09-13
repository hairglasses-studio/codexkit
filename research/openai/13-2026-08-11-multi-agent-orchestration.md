# 13 Multi-agent orchestration and Spark probe, 2026-08-11

Retrieval and probe date: 2026-08-11.

This note separates current official OpenAI facts from harness conclusions and
time-bound local observations. It is a cited summary, not a copy of the Codex
manual.

## Official facts

- OpenAI announced GPT-5.3-Codex-Spark as a research preview and a smaller
  GPT-5.3-Codex variant designed for real-time coding. The announcement reports
  more than 1,000 tokens per second on the low-latency serving path. At
  launch it has a 128k context window and is text-only.
  [Source](https://openai.com/index/introducing-gpt-5-3-codex-spark/)
- Spark preview usage has its own rate limit and does not count against standard
  Codex limits. OpenAI also warns that high demand can reduce access or cause
  queuing. The separate bucket is a product rule; it does not promise a fixed
  amount of capacity.
  [Source](https://openai.com/index/introducing-gpt-5-3-codex-spark/)
- OpenAI describes Spark's default working style as lightweight and targeted,
  and says it does not automatically run tests unless asked. The announcement
  presents longer-horizon execution and real-time collaboration as
  complementary modes, and discusses future delegation to background
  subagents or parallel fanout.
  [Source](https://openai.com/index/introducing-gpt-5-3-codex-spark/)
- Local Codex clients support custom agents with task-specific instructions and
  model configuration. Personal agents live under `~/.codex/agents/` and
  project agents under `.codex/agents/`; each standalone TOML file declares
  `name`, `description`, and `developer_instructions`, and may set `model`,
  `model_reasoning_effort`, `sandbox_mode`, MCP servers, and skills. Global
  settings include a concurrent-thread cap and default subagent model and
  reasoning effort, while explicit spawn values take precedence.
  [Source](https://learn.chatgpt.com/docs/agent-configuration/subagents)
- The official subagent examples use Spark for a read-only explorer and a
  narrowly scoped UI fixer, but those examples are illustrations rather than a
  mandate for every workload.
  [Source](https://learn.chatgpt.com/docs/agent-configuration/subagents)

## Harness inferences

The following are local design choices derived from the official capabilities;
they are not OpenAI requirements:

- Use a frontier model as the leader for ambiguous planning, ownership
  decisions, synthesis, and final verification. Use Spark workers for narrow,
  bounded exploration or targeted edits where low latency has real value.
- Give every worker a disjoint task packet, explicit file ownership, a small
  tool surface, and a required evidence return. A leader remains responsible
  for reconciling conflicts and deciding whether the result is usable.
- Treat the standard and Spark usage limits as separate budgets. Never infer
  general concurrency or quota guarantees from one account's displayed
  percentages.
- Begin with sequential execution or low fanout, then raise concurrency only
  after process creation and result quality are stable. A fast model does not
  make external process creation reliable.
- Ask Spark explicitly for tests when it owns a change, because its documented
  default style does not automatically run them.

## Time-bound local probe evidence

These facts describe one managed local environment on 2026-08-11. They are not
universal Codex documentation, entitlement promises, or stable capacity data.

- Codex CLI `0.147.0` accepted `gpt-5.3-codex-spark` as a model selection.
- The local app-server evidence reported 16% used in the standard bucket and 4%
  used in the Spark bucket (approximately 84% and 96% headroom), with a
  seven-day reset shown for August 18.
- A four-way external fanout returned one usable result. A two-way retry
  returned one usable result and one process-create failure. Equivalent
  sequential launches worked.
- The first managed Spark launch at 2026-08-11 13:14:43 rewrote the tracked
  `.codex/config.toml` during Ralph `agent-launch` project-config alignment,
  inserting approval, sandbox, authentication, and app defaults. The root
  coordinator restored the tracked minimal configuration.

The process failures support a conservative fanout policy, not a claim about
Spark inference reliability. The usage percentages and reset date must be
discarded as soon as a new observation replaces them.

The launcher mutation is a preflight hazard for any high-volume or
write-capable fanout. Before launching, record the tracked project-config hash;
after launch, compare it before allowing workers to write. Any unexpected
change stops the fanout for review. This hash canary is a harness safety
inference based on the observed local mutation, not an official OpenAI
requirement.

## Adoption boundary

Multi-agent availability is not sufficient evidence for a fleet-wide default.
Adoption requires a clean config-hash canary, reliable low-fanout process
creation, narrow custom-agent definitions, explicit standard-versus-Spark
budget reporting, and successful sequential fallback. Preserve the frontier
leader as the authority for synthesis and validation until those gates have
repeatable local evidence.
