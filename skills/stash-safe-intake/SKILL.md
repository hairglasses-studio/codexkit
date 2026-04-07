---
name: stash-safe-intake
description: Operate local StashApp ingest and safe SexyForums thread matching. Use when Codex needs to check Stash health or auth, run the new-library scan/identify/auto-tag workflow, normalize SexyForums thread URLs, search SexyForums for performer-name matches, or merge live search results with seed thread lists without extracting gated or download links.
---

# Stash Safe Intake

Use the workspace MCP tools to operate the local Stash workflow and the read-only SexyForums matcher.

## Stash Workflow

1. Call `get_stashapp_health` first.
2. If health returns `status="unauthorized"` or `status="unreachable"`, report that directly before attempting ingest.
3. Call `run_new_library_ingest()` with no `paths` to use the live configured Stash roots unless the user explicitly narrows scope.
4. Pass explicit `paths` only when they are absolute filesystem paths.
5. Leave `wait_for_jobs=true` unless the user explicitly wants a queued-only scan.
6. Summarize stage order, job ids, failed stage name, and final status instead of dumping raw payloads.

## SexyForums Workflow

1. Use `search_sexyforums_threads(query, limit)` for live thread discovery from a performer or search string.
2. Use `normalize_sexyforums_thread_urls(urls)` for user-supplied thread lists so page and post variants collapse to canonical thread URLs.
3. Use `find_sexyforums_thread_matches(search_terms, seed_urls, limit_per_term)` when both live search results and seed URLs should be merged and deduped.
4. Use `get_sexyforums_session_status()` only to report whether a stored browser session exists and appears logged in.
5. Report thread ids, titles, canonical thread URLs, counts, duplicates collapsed, and invalid URLs.

## Output Rules

- Use exact current URLs and absolute dates when reporting live search results.
- Prefer concise summaries over raw HTML or large JSON blobs.
- When seed URLs and live search disagree, report both the normalized seed count and the merged unique-thread count.

## Boundaries

- Do not extract, reveal, validate, or summarize hidden download links.
- Do not inspect gated thread-body content beyond the safe read-only search and canonicalization workflow.
- Do not treat `missing_storage_state` as a fatal error; report it as the session status.
- If the user asks for download-link extraction, refuse that part and continue with search, normalization, or Stash operations if still useful.
