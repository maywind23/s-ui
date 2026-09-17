#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
target_repo="${1:-.}"
patch_file="$script_dir/surge-snell.patch"

git -C "$target_repo" apply --check "$patch_file"
git -C "$target_repo" apply "$patch_file"

echo "Dedicated Surge Snell patch applied to $target_repo"
