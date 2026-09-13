# Official upstream research feeds

`feeds.json` is the allowlisted, versioned input to Codexkit's research-refresh
tooling. It tracks official documentation and release metadata without making
the harness a mirror of an upstream manual. Fetches are credential-free,
HTTPS-only, size-capped, redirect-constrained, and in memory unless an operator
explicitly asks the CLI to write sanitized receipts.

The registry records exact source URLs, expected media types, and check
cadences. Cross-host redirects are denied unless the exact lower-case hostname
appears in `allowed_redirect_domains`; same-host redirects remain allowed. The
Codex manual is the one provider-digested feed: its final body must match the
single `x-content-sha256` response header. The digest value is intentionally not
pinned in this repository because the provider rotates it when the manual
changes.

## Tracked source families

| Family | Feed IDs | Review purpose |
| --- | --- | --- |
| Codex docs | `openai.codex.manual`, `openai.codex.changelog`, `openai.codex.feature-maturity`, `openai.codex.config-reference`, `openai.codex.subagents`, `openai.codex.hooks`, `openai.codex.app-server`, `openai.codex.sdk` | Detect provider behavior, maturity, configuration, extension, and orchestration changes. |
| Codex releases and models | `openai.codex.latest-release`, `openai.codex.models` | Detect CLI releases and changes to the official Codex model catalog. |
| MCP Go SDK | `mcp.go-sdk.latest-release`, `mcp.go-sdk.readme`, `mcp.go-sdk.protocol-docs` | Detect SDK releases, compatibility claims, and protocol-facing implementation guidance. |
| MCP specification | `mcp.spec.latest-release`, `mcp.spec.schema.2026-07-28` | Detect a new protocol release and fingerprint the schema for the currently reviewed release. |

The MCP schema URL is pinned to the matching release tag deliberately. When
`mcp.spec.latest-release` changes, review the release, add or replace the
versioned schema feed, and update consumers only after semantic review. Do not
silently retarget the schema feed to `draft` or an unreviewed branch path.

## Explicit network refresh

The tracked body-free receipt aggregate provides a reviewable bootstrap for a
new checkout. Before updating it, run a non-mutating network refresh first:

```sh
cd <harness-root>/codexkit
GOWORK=off go run ./cmd/codexkit research refresh <harness-root> --json
```

The command returns candidate hashes and change flags but does not write them.
Review reachability errors, redirects, media types, provider-integrity results,
and semantic changes. Only then opt in to the body-free receipt update:

```sh
GOWORK=off go run ./cmd/codexkit research refresh <harness-root> --write
```

Use repeatable `--source <feed-id>` flags to narrow either command. No API token
or other authentication belongs in the registry, command, output, or receipt.
The refresh client does not consult proxy credentials, and receipts exclude
response bodies, URL queries, fragments, cookies, and authorization state.

## Offline freshness gate

Once reviewed receipts exist, this command performs no network request:

```sh
cd <harness-root>/codexkit
GOWORK=off go run ./cmd/codexkit research freshness <harness-root> --json
```

It compares receipt timestamps with each feed's `cadence_hours` and exits
nonzero when any selected feed is stale or missing. Freshness proves only that a
valid receipt was recorded within the cadence. It does not prove that a changed
upstream was semantically reviewed or adopted.

This freshness check is a required `make ci` gate. Feeds with a 24-hour cadence
therefore stop publication once their receipt expires; that is intentional.
Run the dry-run, review any semantic changes, and explicitly write the
body-free aggregate before retrying CI.

## Semantic review and task packets

Text, Markdown, and HTML feeds are normalized for line endings, trailing
horizontal whitespace, and outer blank lines before semantic hashing. GitHub
latest-release responses are reduced to release identity and metadata plus a
hash of the normalized release body. A semantic hash change is therefore a
review signal, not permission to rewrite configuration, regenerate projections,
or copy documentation into this repository.

The coordinator turns changed feed results into narrow task packets. A packet
should contain the feed ID, official source URL, prior and candidate semantic
hashes, check time, affected harness surface, ownership class, and the question
the reviewer must resolve. It must not contain a fetched manual, credentials,
cookies, or opaque runtime state. Reviewing agents consume packets as follows:

1. Re-fetch only the named official source through the explicit network path.
2. Compare the changed section with the last durable summary and relevant
   harness contract.
3. Label direct provider facts separately from harness inferences.
4. Propose the smallest owner-scoped change and its verifier; do not mutate a
   consumer merely because the feed changed.
5. Update cited summaries, decision records, and the sanitized receipt only
   after review. Never check in a copied upstream manual.

This keeps the durable record small and reviewable while still giving agents
enough provenance to investigate a change precisely.
