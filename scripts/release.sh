#!/usr/bin/env bash
# Release Huntsman from your machine. The release itself runs on GitHub Actions (.github/workflows/release.yml).
set -euo pipefail

repo=TulipFarm/huntsman
bump=auto
version=""
yes=false

usage() {
  cat <<USAGE
Usage: scripts/release.sh [auto|patch|minor|major|X.Y.Z] [--yes]

Releases origin/main: writes CHANGELOG.md, tags vX.Y.Z, publishes ghcr.io/tulipfarm/huntsman and creates the
GitHub release. auto (the default) picks the bump from the commit messages. Needs gh; git-cliff is optional and
adds a preview of the version and release notes.
USAGE
}

for arg in "$@"; do
  case "$arg" in
    auto | patch | minor | major) bump=$arg ;;
    v[0-9]* | [0-9]*)
      version=${arg#v}
      [[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "Version $arg is not X.Y.Z." >&2; exit 2; }
      ;;
    -y | --yes) yes=true ;;
    -h | --help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

command -v gh >/dev/null || { echo "Install the GitHub CLI: https://cli.github.com" >&2; exit 1; }
gh auth status --hostname github.com >/dev/null 2>&1 || { echo "Run gh auth login first." >&2; exit 1; }

remote=$(git remote -v | awk -v r="$repo" 'tolower($2) ~ tolower(r) && $3 == "(fetch)" { print $1; exit }')
[ -n "$remote" ] || { echo "No git remote points at $repo." >&2; exit 1; }
git fetch --quiet --tags "$remote" main
sha=$(git rev-parse FETCH_HEAD)
latest=$(git describe --tags --abbrev=0 --match 'v[0-9]*' "$sha" 2>/dev/null || true)
range=${latest:+$latest..}$sha

changes=$(git log --format='  %h %s' --invert-grep --grep='^chore(release):' "$range")
[ -n "$changes" ] || { echo "Nothing to release: main has no changes since ${latest}."; exit 0; }

# Release commits don't trigger CI, so check the last code commit.
code=$(git log -1 --format=%H --invert-grep --grep='^chore(release):' "$sha")
ci=$(gh run list --repo "$repo" --workflow ci.yml --commit "$code" --json status,conclusion \
  --jq 'if any(.[]; .conclusion == "success") then "success"
        elif any(.[]; .status != "completed") then "running"
        else "failed" end')
case "$ci" in
  success) ;;
  running) echo "CI is still running on ${code:0:7}. Try again when it passes." >&2; exit 1 ;;
  *) echo "CI has not passed on ${code:0:7}: https://github.com/$repo/actions/workflows/ci.yml" >&2; exit 1 ;;
esac

echo "Releasing main at ${sha:0:7} (previous release: ${latest:-none})"
echo
echo "$changes"
echo
if command -v git-cliff >/dev/null; then
  if [ -n "$version" ]; then next=v$version; else next=$(git cliff --bump "$bump" --bumped-version "$range" 2>/dev/null); fi
  echo "Next version: $next"
  echo
  git cliff --tag "$next" --strip all "$range" 2>/dev/null | sed 's/^./  &/'
  echo
else
  echo "Next version: ${version:-chosen by the workflow ($bump)}. Install git-cliff to preview the release notes."
  echo
fi

if ! $yes; then
  read -r -p "Start the release on GitHub Actions? [y/N] " answer
  [[ $answer =~ ^[Yy]$ ]] || { echo "Cancelled."; exit 1; }
fi

started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
gh workflow run release.yml --repo "$repo" --ref main -f bump="$bump" -f version="$version" -f sha="$sha" >/dev/null

run=""
for _ in $(seq 30); do
  run=$(gh run list --repo "$repo" --workflow release.yml --event workflow_dispatch --limit 5 \
    --json databaseId,createdAt --jq "map(select(.createdAt >= \"$started\")) | last | .databaseId // empty")
  [ -n "$run" ] && break
  sleep 2
done
[ -n "$run" ] || { echo "Started, but couldn't find the run: https://github.com/$repo/actions/workflows/release.yml" >&2; exit 1; }

echo "Watching https://github.com/$repo/actions/runs/$run"
if ! gh run watch "$run" --repo "$repo" --exit-status --interval 15 >/dev/null; then
  echo "The release failed. Logs: gh run view $run --repo $repo --log-failed" >&2
  exit 1
fi

git fetch --quiet --tags "$remote" main
tag=$(git describe --tags --abbrev=0 --match 'v[0-9]*' FETCH_HEAD)
echo "Released $tag"
echo "  https://github.com/$repo/releases/tag/$tag"
echo "  docker pull ghcr.io/tulipfarm/huntsman:${tag#v}"
