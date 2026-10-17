# Skill resource compatibility

Canonical skills can contain references, scripts, assets, examples and templates.
Generated Claude/plugin projections retain these directories as relative links
to the canonical owner. They do not execute helper scripts. Existing differing
resources or escaping links refuse the sync before instruction files change.
Identical existing directories are preserved.

For an existing global skill with independently verified source ownership:

```text
codexkit skills resources /absolute/source-skill /absolute/installed-skill
codexkit skills resources /absolute/source-skill /absolute/installed-skill --apply --expected-plan=SHA256
```

The first command is read-only. The hash binds both instruction files, resource
bytes and target state. Apply rechecks the hash and creates missing links
exclusively through a pinned destination directory. It never rewrites SKILL.md,
invocation policy, configuration, existing resources or authentication. Both
skill directories must exist and be canonical paths. The caller reviews the
source/target mapping; matching basenames do not establish equivalence.

Reports enumerate source, target, digest, link text, state and errors. Partial
creation retains its explicit created states and errors; do not retry uncertain
operations blindly. For rollback, unlink only links created by this transaction
after confirming their current identity and exact link text; preserve changed
links and every original file. Source changes require a fresh reviewed plan.

Source trees are bounded to 4,096 entries, 32 MiB total per resource directory
and 8 MiB per file. Special files and escaping/dangling symlinks are refused.
The links follow their canonical owner after installation; plan hashes establish
the observed installation generation, not an immutable future snapshot.

Codex/Claude native discovery, helper execution and running-client reload must
be verified separately from link presence. Platform-specific renderer tests
state unsupported filesystem capabilities explicitly and retain a fail-closed
negative test; a skipped platform test is not runtime qualification.
