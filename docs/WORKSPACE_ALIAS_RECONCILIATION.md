# Narrow workspace alias reconciliation

`codexkit workspace reconcile-aliases --root /absolute/workspace --alias NAME
--json` previews removal of an explicitly named active symlink alias row from
`workspace/manifest.json`. Repeat `--alias` for up to 32 names. The typed MCP
owner is `workspace_manifest_reconcile_aliases`, accepting `root`, `aliases`,
optional `expected_sha256` and optional boolean `apply`.

Apply requires `--apply --expected-sha256 HASH`, using the exact original
manifest hash from the preview. It removes only the selected rows when each
is active, has its default path, and is an existing symlink resolving to one
active physical canonical Git checkout represented in the same manifest and
contained within the workspace. It rejects duplicate names, ambiguous owners,
escaping or missing targets, inactive aliases, malformed/duplicate JSON keys,
unsupported file types and changed observations. It does not regenerate the
manifest, delete symlinks, move repositories or change repository contents.

Unknown root and retained-row fields, numeric JSON spellings, and retained
row order survive. Whitespace and object-key order may be normalized. The
manifest's existing ordinary permission bits are preserved; group/world
writable or special-mode manifests are refused. Reads are bounded to 1 MiB
through the existing descriptor-based `internal/harnessfs` owner. Linux and
Darwin are supported by that substrate; other platforms refuse. Git identity
checks cap stdout at 8 KiB and bound execution and inherited pipe waits.

The owner takes a nonblocking `.manifest-alias-reconcile.lock`, rechecks the
manifest hash and alias identities, stages the result, writes a private
PREPARED intent receipt, rechecks again, and atomically exchanges the manifest
through `harnessfs`. The displaced original is retained at the receipt's
`preimage_path`; apply verifies both its original bytes/inode and the new
manifest hash before returning APPLIED. Existing readers do not participate
in this lock. Arbitrary writers that ignore it can still race: this is not an
operating-system SHA compare-and-swap. A conflicting or uncertain readback
returns an error and preserves artifacts rather than reporting success.

The durable intent lists prospective staging, preimage and retained paths;
PREPARED alone never proves the exchange happened or what any path contains.
A failed stage is retained rather than deleted. On a lost response, inspect
the manifest and receipt-bound artifact hashes. Do not blindly retry with a
new hash or treat an absent alias as successful completion. Reusing an old
expected hash after application refuses. No automatic cleanup or rollback is
performed by this API.

After a successful reconciliation, consumers such as Scripts
`codex_workflow.canonical_repo` must independently verify their canonical
identity again. This command does not confer workflow registration, claim,
lease or runtime launch authority.
