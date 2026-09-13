# 06 App, IDE, cloud, and worktree surfaces

Primary sources: OAI-CX-060, OAI-CX-061, OAI-CX-062, OAI-CX-063, OAI-CX-064.

## Verified facts

- Desktop Codex can run chats in Local, Worktree, or Cloud environments.
- Codex cloud setup involves ChatGPT sign-in, GitHub connection, repository access, and environment configuration.
- The IDE extension can use open files and selections as prompt context and supports in-place review.
- Worktrees isolate independent chats in the same Git project; scheduled tasks can use dedicated background worktrees for Git repositories.
- Local environments configure setup steps for worktrees through the ChatGPT desktop app.

## Inferences for this harness

- A launcher contract for CLI worktrees is not automatically equivalent to desktop-app worktree behavior. Keep the surface label explicit.
- Cloud guidance should include repository permissions, environment variables, secrets, and internet access as separate controls.
- IDE context ingestion is a convenience surface; do not assume it preserves the same reproducibility as a CLI prompt plus checked-in files.

## Volatile items

- Worktree handoff behavior.
- Scheduled task isolation.
- Cloud environment setup and secret handling.
- Supported IDEs and editor context behavior.
- Generated local-environment file format.

## Migration checks

- For every workflow, label the execution surface: local, worktree, cloud, IDE, SDK, or CI.
- Do not reuse desktop-only local-environment assumptions in CLI automation without an explicit source update.
- Recheck cloud docs before changing secrets or internet-access defaults.
