# Shell discipline

- Never dump environment values (`env`, `printenv`, `set`, or process
  `environ` contents). For an environment inventory use the sanctioned
  `env | cut -d= -f1 | hg-env-hygiene` route. Credential discovery files are
  metadata-only unless their native owner exposes an authorized API.
  A credential-guard refusal cancels the entire Cline run; report it and
  continue only with independent permitted work. Do not retry the refused
  read through Python, another shell, SSH, or a different spelling.
- Keep shell commands syntactically complete with balanced quoting. A
  command the credential guard cannot tokenize is refused before execution.

- Every mutating command is dry-run first. A write path reads back its own
  effect before reporting success; a success reported without read-back is a
  false green.
- Land through a PR: gate the diff locally (`claude-harness/bin/hg-fast-gate
  --repo <abs path> --base origin/main --log LOG`, last `rc=` line `rc=0`,
  `head=` matching the commit), then `claude-harness/bin/hg-land --repo <abs
  path> --local-gate LOG` (branch -> PR -> squash merge). Never force-push;
  "landed" means hg-land's `LANDED <repo> <merge-sha>` line. Single source:
  `claude-harness/claude/rules/fleet-git-push-delegation.md` sections 000/00.
- Never run `git add -A`, `git add .`, `git commit -a`, `git reset --hard`,
  `git checkout .`, `git stash`, or `rm -rf` in a shared checkout. Stage
  commits by explicit path only, and only files you changed in this task.
- Never switch a shared checkout's branch, and never commit in one. Run
  `git branch --show-current` before anything branch-sensitive; every commit
  goes through a temporary `git worktree` off local `main`, with the repo gate
  run first. Preserve the real author and omit AI credits and session trailers
  as specified in `operator-discipline.md`.
- Disable pagination (`git --no-pager`) and redirect stdin from `/dev/null`
  for anything that might prompt. No `wl-copy` / `wl-paste` / `xclip`.
- Never inject keystrokes into another session's terminal. `tmux send-keys`
  is routed only through `scripts/fleet_tmux_guard.py`.
- Batch independent work into one call: `run_commands` takes `commands[]` and
  `read_files` takes `files[]`, and independent calls can go in one turn
  (measured 2026-10-26 over 15,176 teammate iterations: 73% carried a single
  operation; gemini-4-argon averaged 3.5 operations per iteration, gemini-flash 1.25).
- Bound every output at the source (`git log --oneline -n 20`, `--stat`,
  `| head -c 4000`), never re-read a file already in context (53% of
  `read_files` paths were same-session re-reads), and start anything that may
  pass the 30 s tool timeout in the background and poll it once (277 calls
  died at that timeout).
