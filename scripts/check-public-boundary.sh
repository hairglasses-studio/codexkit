#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

failed=0

mark_failed() {
  failed=1
}

echo "== tracked private markers =="
# Encoded so the guard does not match itself.
private_markers=(
  L2hvbWUvaGc=
  YXJjaGdsYXNzZXM=
  c2VjcmV0c3R1ZGlvcw==
  cnVubXlsaWZl
  bWVzbWVy
  Y29iYg==
  b3BlcmF0b3JjaGF0
  aGFpcmdsYXNzZXMtc3R1ZGlvL2pvYmI=
  bWl0Y2htaXRjaGVsbA==
  bWl0Y2hAaGFpcmdsYXNzZXMuc3R1ZGlv
  bWl0Y2htaXRjaGVsbHdvcmtpbnF1aXJpZXM=
  Z21haWwuY29t
  bGlua2VkaW4uY29tL2lu
  bGlua2VkaW4uY29tL21lc3NhZ2luZw==
)

for encoded in "${private_markers[@]}"; do
  marker="$(printf '%s' "$encoded" | base64 -d)"
  if git grep -n -i --fixed-strings -e "$marker" -- . ':!scripts/check-public-boundary.sh'; then
    mark_failed
  fi
done

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
