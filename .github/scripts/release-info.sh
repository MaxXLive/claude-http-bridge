#!/usr/bin/env bash
# Computes the next release version and its changelog from git tags and commits.
#
# Versions follow v<major>.<minor>.<patch>. Major and minor come from the VERSION file; the patch number
# continues from the highest existing tag for them and starts at 0 when VERSION changes.
#
# Outputs (written to $GITHUB_OUTPUT when set, otherwise printed):
#   version    e.g. 2.0.3
#   tag        e.g. v2.0.3
#   previous   previous tag for this major.minor (empty for the first release)
#   released   "true" if HEAD already carries a release tag (re-run of a finished release)
#   relevant   "true" if there are commits worth releasing (anything but docs/ci/test/style)
# The changelog is written to the file given as the first argument (default: CHANGELOG.md).
set -euo pipefail

changelog_file="${1:-CHANGELOG.md}"
base=$(tr -d '[:space:]' < VERSION)
if [[ ! "$base" =~ ^[0-9]+\.[0-9]+$ ]]; then
	echo "VERSION must contain <major>.<minor>, e.g. 2.0" >&2
	exit 1
fi

prefix="v${base}."
released=false
if git tag --points-at HEAD | grep -q "^${prefix}[0-9]\+$"; then
	released=true
fi

previous=$(git tag --list "${prefix}*" | grep "^${prefix}[0-9]\+$" | sort -V | tail -n 1 || true)
if [[ -z "$previous" ]]; then
	next=0
else
	next=$(( ${previous#"$prefix"} + 1 ))
fi
version="${base}.${next}"
tag="v${version}"

# Changelog: commits since the nearest release tag (of any Minecraft version), grouped by conventional commit type.
last_any=$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || true)
range="HEAD"
if [[ -n "$last_any" ]]; then
	range="${last_any}..HEAD"
fi

features=()
fixes=()
other=()
while IFS=$'\t' read -r hash subject; do
	[[ -z "$hash" ]] && continue
	if [[ "$subject" =~ ^(feat|fix|perf|refactor|docs|build|ci|chore|test|style)(\([^\)]*\))?!?:[[:space:]]*(.*)$ ]]; then
		type="${BASH_REMATCH[1]}"
		text="${BASH_REMATCH[3]}"
	else
		type=""
		text="$subject"
	fi
	line="- ${text} (${hash})"
	case "$type" in
		feat) features+=("$line") ;;
		fix | perf) fixes+=("$line") ;;
		docs | ci | test | style) ;; # not relevant for server owners
		*) other+=("$line") ;;
	esac
done < <(git log "$range" --no-merges --format=$'%h\t%s')

{
	echo "Bridge API $(sed -n 's/^	apiVersion = //p' version.go) (accepts clients down to API $(sed -n 's/^	apiMin = //p' version.go)). Download the file for your system, no installation needed."
	echo
	if ((${#features[@]})); then
		echo "### Features"
		printf '%s\n' "${features[@]}"
		echo
	fi
	if ((${#fixes[@]})); then
		echo "### Fixes"
		printf '%s\n' "${fixes[@]}"
		echo
	fi
	if ((${#other[@]})); then
		echo "### Changes"
		printf '%s\n' "${other[@]}"
		echo
	fi
	if ((${#features[@]} + ${#fixes[@]} + ${#other[@]} == 0)); then
		echo "No functional changes."
		echo
	fi
	if [[ -n "$last_any" && -n "${GITHUB_REPOSITORY:-}" ]]; then
		echo "**Full changelog:** ${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY}/compare/${last_any}...${tag}"
	fi
} > "$changelog_file"

relevant=false
if ((${#features[@]} + ${#fixes[@]} + ${#other[@]} > 0)); then
	relevant=true
fi

outputs=$(printf 'version=%s
tag=%s
previous=%s
released=%s
relevant=%s
' "$version" "$tag" "$previous" "$released" "$relevant")
if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
	echo "$outputs" >> "$GITHUB_OUTPUT"
fi
echo "$outputs"
