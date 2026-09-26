#!/bin/bash
# Checks staged Go and shell files without validating a different worktree snapshot.

set -euo pipefail

script_dir="$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly script_dir
repo_root="$(CDPATH='' cd -- "$script_dir/.." && pwd)"
readonly repo_root
cd -- "$repo_root"

if [[ -z "$(git diff --cached --name-only)" ]]; then
	exit 0
fi
if ! git diff --quiet || [[ -n "$(git ls-files --others --exclude-standard)" ]]; then
	printf '%s\n' 'Stage or remove all changes before committing; checks run against the staged snapshot.' >&2
	exit 1
fi

git diff --cached --check
go_files=()
shell_files=()
while IFS= read -r -d '' file; do
	case "$file" in
	*.go) go_files+=("$file") ;;
	*.sh | scripts/hooks/*) shell_files+=("$file") ;;
	esac
done < <(git diff --cached --name-only --diff-filter=ACMR -z)

if ((${#go_files[@]} > 0)); then
	gofmt_diff="$(gofmt -d "${go_files[@]}")"
	if [[ -n "$gofmt_diff" ]]; then
		printf '%s\n' 'Go files need gofmt:' "$gofmt_diff" >&2
		exit 1
	fi
fi
if ((${#shell_files[@]} > 0)); then
	shfmt_output="$(go tool shfmt -l "${shell_files[@]}")"
	if [[ -n "$shfmt_output" ]]; then
		printf '%s\n' 'Shell files need shfmt.' >&2
		exit 1
	fi
fi

python3 scripts/lint_binaries.py
python3 scripts/update_agents_file_index.py --check
