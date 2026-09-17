#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
target_repo="${1:-.}"
patch_file="$script_dir/surge-snell.patch"

git -C "$target_repo" apply --reverse --check "$patch_file"
git -C "$target_repo" apply --reverse "$patch_file"

echo "Dedicated Surge Snell patch rolled back from $target_repo"
