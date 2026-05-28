#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

failed=0

mark_failed() {
  failed=1
}

echo "== local denylist =="
if [[ -n "${CODEXKIT_PRIVATE_MARKERS_FILE:-}" ]]; then
  if [[ ! -f "$CODEXKIT_PRIVATE_MARKERS_FILE" ]]; then
    echo "CODEXKIT_PRIVATE_MARKERS_FILE does not exist: $CODEXKIT_PRIVATE_MARKERS_FILE" >&2
    mark_failed
  else
    while IFS= read -r marker || [[ -n "$marker" ]]; do
      [[ -z "$marker" || "$marker" =~ ^[[:space:]]*# ]] && continue
      if git grep -n -i --fixed-strings -e "$marker" -- . ':!scripts/check-public-boundary.sh'; then
        mark_failed
      fi
    done <"$CODEXKIT_PRIVATE_MARKERS_FILE"
  fi
else
  echo "CODEXKIT_PRIVATE_MARKERS_FILE not set; running tracked generic checks only"
fi

echo "== absolute user paths =="
if git grep -n -E -e '(/home/[A-Za-z0-9._-]+|/Users/[A-Za-z0-9._-]+|[A-Za-z]:\\Users\\[^[:space:]]+)' -- . ':!scripts/check-public-boundary.sh'; then
  mark_failed
fi

echo "== private workspace path examples =="
if git grep -n -E -e '(~|\$HOME)/hairglasses-studio' -- . ':!scripts/check-public-boundary.sh'; then
  mark_failed
fi

echo "== live account URLs =="
if git grep -n -E -e 'linkedin\.com/(in|messaging|jobs)' -- . ':!scripts/check-public-boundary.sh'; then
  mark_failed
fi

echo "== non-example emails =="
email_hits="$(mktemp)"
trap 'rm -f "$email_hits"' EXIT
if git grep -n -E -e '[[:alnum:]_.%+-]+@[[:alnum:].-]+\.[[:alpha:]]{2,}' -- . ':!scripts/check-public-boundary.sh' >"$email_hits"; then
  if grep -v -E '@example\.(com|org|net|test)' "$email_hits"; then
    mark_failed
  fi
fi

echo "== symlink portability =="
while IFS= read -r path; do
  [[ -L "$path" ]] || continue
  target="$(readlink "$path")"
  if [[ "$target" = /* || "$target" == ../* ]]; then
    printf 'non-portable symlink: %s -> %s\n' "$path" "$target"
    mark_failed
  fi
  if [[ ! -e "$path" ]]; then
    printf 'broken symlink: %s -> %s\n' "$path" "$target"
    mark_failed
  fi
done < <(git ls-files -s | awk '$1 ~ /^120000/ {print $4}')

echo "== gitleaks =="
if command -v gitleaks >/dev/null 2>&1; then
  gitleaks detect --source . --no-git --redact
else
  echo "gitleaks not installed; skipping local secret scan"
fi

if [[ "$failed" -ne 0 ]]; then
  echo "public boundary check failed" >&2
  exit 1
fi

echo "public boundary checks passed"
