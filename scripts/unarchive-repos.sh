#!/usr/bin/env bash
# Un-archive all archived repos in the hairglasses-studio org.
# Usage: GITHUB_TOKEN=ghp_xxx ./scripts/unarchive-repos.sh
#
# Requires a GitHub PAT with "admin:org" + "repo" scopes,
# or a fine-grained token with Administration (write) on each repo.

set -euo pipefail

ORG="hairglasses-studio"

ARCHIVED_REPOS=(
  dotfiles-mcp
  process-mcp
  tmux-mcp
  systemd-mcp
)

if [[ -z "${GITHUB_TOKEN:-}" ]]; then
  echo "Error: GITHUB_TOKEN is not set." >&2
  echo "Usage: GITHUB_TOKEN=ghp_xxx $0" >&2
  exit 1
fi

for repo in "${ARCHIVED_REPOS[@]}"; do
  echo -n "Un-archiving ${ORG}/${repo}... "
  http_code=$(curl -s -o /dev/null -w "%{http_code}" \
    -X PATCH \
    -H "Authorization: Bearer ${GITHUB_TOKEN}" \
    -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    "https://api.github.com/repos/${ORG}/${repo}" \
    -d '{"archived": false}')

  if [[ "$http_code" == "200" ]]; then
    echo "done"
  else
    echo "FAILED (HTTP ${http_code})"
  fi
done

echo "Finished."
