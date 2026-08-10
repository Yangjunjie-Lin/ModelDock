#!/usr/bin/env sh
set -eu

project_root="$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)"
mkdir -p "$project_root/data/postgres" "$project_root/data/redis" "$project_root/logs"

printf 'Runtime directories are ready under %s\n' "$project_root"
printf 'On rootless Linux, ensure container users can write these directories.\n'

