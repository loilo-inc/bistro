#!/usr/bin/env bash
set -euo pipefail

module="$(awk '/^module /{print $2}' go.mod)"
mod_major="1"
if [[ "$module" =~ /v([0-9]+)$ ]]; then
  mod_major="${BASH_REMATCH[1]}"
fi

tag="${GITHUB_REF_NAME}"
if [[ "$tag" =~ ^v([0-9]+)\. ]]; then
  tag_major="${BASH_REMATCH[1]}"
else
  echo "::error ::Tag must start with v<major>., got: ${tag}"
  exit 1
fi

if [[ "$tag_major" != "$mod_major" ]]; then
  echo "::error ::Tag major v${tag_major} does not match go.mod module major v${mod_major} (${module})"
  exit 1
fi
