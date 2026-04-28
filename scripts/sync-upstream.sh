#!/usr/bin/env bash
set -euo pipefail

# =============================================================================
# sync-upstream.sh — sync bonjourcuong/multica `main` with upstream multica-ai/multica
#
# Contract (see PKM-CUONG/GROWTH/PROJECTS/multica-fork/adrs/2026-04-28-upstream-sync-strategy.md):
#   - Idempotent. Safe to re-run.
#   - Dry-run by default. Reports diff and exits without merging.
#   - --apply prompts for explicit y/N confirmation before merging.
#   - --push only valid with --apply. Pushes to origin/main after a clean merge.
#   - --report-only is cron-friendly: fetch + report, exit 0, no prompt.
#   - Never rebases. Never force-pushes. Never amends.
#   - Aborts cleanly on conflict via `git merge --abort`.
#
# Usage:
#   ./scripts/sync-upstream.sh                  # dry-run, prints report
#   ./scripts/sync-upstream.sh --apply          # interactive merge after prompt
#   ./scripts/sync-upstream.sh --apply --push   # merge + push to origin/main
#   ./scripts/sync-upstream.sh --report-only    # cron mode, report and exit
#   ./scripts/sync-upstream.sh --help
#
# Exit codes:
#   0  ok (or up-to-date, or dry-run completed, or user said no)
#   1  user aborted at prompt
#   2  dirty working tree
#   3  not on main branch (or detached HEAD)
#   4  merge conflict (aborted cleanly)
#   5  no upstream remote configured
#   6  invalid arguments
# =============================================================================

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# --- color helpers (only when stdout is a tty) -------------------------------
if [ -t 1 ]; then
  C_RED=$'\033[31m'; C_GREEN=$'\033[32m'; C_YELLOW=$'\033[33m'
  C_BLUE=$'\033[34m'; C_BOLD=$'\033[1m'; C_RESET=$'\033[0m'
else
  C_RED=""; C_GREEN=""; C_YELLOW=""; C_BLUE=""; C_BOLD=""; C_RESET=""
fi

info()  { printf "%s[info]%s  %s\n" "$C_BLUE"   "$C_RESET" "$*"; }
ok()    { printf "%s[ok]%s    %s\n" "$C_GREEN"  "$C_RESET" "$*"; }
warn()  { printf "%s[warn]%s  %s\n" "$C_YELLOW" "$C_RESET" "$*"; }
fail()  { printf "%s[fail]%s  %s\n" "$C_RED"    "$C_RESET" "$*" >&2; }
hr()    { printf -- "----------------------------------------------------------------\n"; }

usage() {
  sed -n '4,28p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

# --- arg parsing -------------------------------------------------------------
APPLY=0
PUSH=0
REPORT_ONLY=0

while [ $# -gt 0 ]; do
  case "$1" in
    --apply)        APPLY=1 ;;
    --push)         PUSH=1 ;;
    --report-only)  REPORT_ONLY=1 ;;
    -h|--help)      usage; exit 0 ;;
    *)              fail "unknown argument: $1"; usage; exit 6 ;;
  esac
  shift
done

if [ "$PUSH" = 1 ] && [ "$APPLY" = 0 ]; then
  fail "--push requires --apply"
  exit 6
fi
if [ "$REPORT_ONLY" = 1 ] && [ "$APPLY" = 1 ]; then
  fail "--report-only and --apply are mutually exclusive"
  exit 6
fi

# --- preflight ---------------------------------------------------------------
UPSTREAM_REMOTE="upstream"
UPSTREAM_BRANCH="main"
ORIGIN_REMOTE="origin"
LOCAL_BRANCH="main"

info "repo: $REPO_ROOT"

if ! git remote get-url "$UPSTREAM_REMOTE" >/dev/null 2>&1; then
  fail "no '$UPSTREAM_REMOTE' remote configured. Add it: git remote add upstream https://github.com/multica-ai/multica.git"
  exit 5
fi

CURRENT_BRANCH="$(git rev-parse --abbrev-ref HEAD)"
if [ "$CURRENT_BRANCH" != "$LOCAL_BRANCH" ]; then
  fail "current branch is '$CURRENT_BRANCH', expected '$LOCAL_BRANCH'. Switch first: git checkout $LOCAL_BRANCH"
  exit 3
fi

if ! git diff --quiet --ignore-submodules HEAD -- || ! git diff --cached --quiet --ignore-submodules HEAD --; then
  fail "working tree is dirty. Commit or stash before syncing:"
  git status --short
  exit 2
fi

# --- fetch -------------------------------------------------------------------
info "fetching $UPSTREAM_REMOTE/$UPSTREAM_BRANCH..."
git fetch --quiet "$UPSTREAM_REMOTE" "$UPSTREAM_BRANCH"

LOCAL_HEAD="$(git rev-parse "$LOCAL_BRANCH")"
UPSTREAM_HEAD="$(git rev-parse "$UPSTREAM_REMOTE/$UPSTREAM_BRANCH")"
MERGE_BASE="$(git merge-base "$LOCAL_HEAD" "$UPSTREAM_HEAD")"

BEHIND="$(git rev-list --count "$LOCAL_HEAD..$UPSTREAM_HEAD")"
AHEAD="$(git rev-list --count "$UPSTREAM_HEAD..$LOCAL_HEAD")"

# --- report ------------------------------------------------------------------
hr
printf "%sUpstream sync report%s — %s\n" "$C_BOLD" "$C_RESET" "$(date -Iseconds)"
hr
printf "  local  %s @ %s\n" "$LOCAL_BRANCH"           "${LOCAL_HEAD:0:12}"
printf "  upstr  %s/%s @ %s\n" "$UPSTREAM_REMOTE" "$UPSTREAM_BRANCH" "${UPSTREAM_HEAD:0:12}"
printf "  base   %s\n"                                "${MERGE_BASE:0:12}"
printf "  behind %s commits | ahead %s commits\n"     "$BEHIND" "$AHEAD"
hr

if [ "$BEHIND" = "0" ]; then
  ok "already up to date with $UPSTREAM_REMOTE/$UPSTREAM_BRANCH. Nothing to do."
  exit 0
fi

printf "\n%sCommits to merge (%s):%s\n" "$C_BOLD" "$BEHIND" "$C_RESET"
git log --oneline --no-decorate "$LOCAL_HEAD..$UPSTREAM_HEAD"

printf "\n%sFile diffstat:%s\n" "$C_BOLD" "$C_RESET"
git diff --stat "$LOCAL_HEAD..$UPSTREAM_HEAD" | tail -n 40

printf "\n%sChange categories:%s\n" "$C_BOLD" "$C_RESET"
git diff --name-only "$LOCAL_HEAD..$UPSTREAM_HEAD" | awk '
  /^server\//                                                      { back++;  next }
  /^(apps\/(web|desktop)|packages)\//                              { front++; next }
  /^Dockerfile|^docker-compose|^\.github\/workflows\/|^scripts\/|^Makefile$|package\.json$|pnpm-lock\.yaml$|^\.env\.example$/ { infra++; next }
  /\.md$|^docs\//                                                  { docs++;  next }
  { other++ }
  END {
    printf "  back:  %d (server/)\n  front: %d (apps/web,desktop + packages/)\n  infra: %d (docker, ci, scripts, build)\n  docs:  %d (markdown)\n  other: %d\n", back+0, front+0, infra+0, docs+0, other+0
  }
'
hr

# --- report-only mode (cron) -------------------------------------------------
if [ "$REPORT_ONLY" = 1 ]; then
  ok "report-only mode. Not merging. Exit 0."
  exit 0
fi

# --- dry-run (default) -------------------------------------------------------
if [ "$APPLY" = 0 ]; then
  warn "dry-run. To merge, re-run with --apply (you will be prompted to confirm)."
  exit 0
fi

# --- apply: prompt for confirmation ------------------------------------------
printf "\n%sYou are about to merge %s commits from %s/%s into %s.%s\n" \
  "$C_BOLD" "$BEHIND" "$UPSTREAM_REMOTE" "$UPSTREAM_BRANCH" "$LOCAL_BRANCH" "$C_RESET"
if [ "$PUSH" = 1 ]; then
  printf "%sAfter merge, this script will push to %s/%s.%s\n" \
    "$C_YELLOW" "$ORIGIN_REMOTE" "$LOCAL_BRANCH" "$C_RESET"
fi
printf "Type %sy%s to proceed, anything else to abort: " "$C_GREEN" "$C_RESET"

if ! [ -t 0 ]; then
  fail "stdin is not a tty. --apply requires an interactive terminal. Use --report-only for cron."
  exit 1
fi
read -r ANSWER
if [ "$ANSWER" != "y" ] && [ "$ANSWER" != "Y" ]; then
  warn "aborted by user."
  exit 1
fi

# --- merge -------------------------------------------------------------------
SYNC_TS="$(date +%Y-%m-%d)"
MERGE_MSG="chore: sync upstream multica-ai/multica $SYNC_TS

Sync $BEHIND commits from upstream/main into bonjourcuong/main.
Base: ${MERGE_BASE:0:12}
Upstream HEAD: ${UPSTREAM_HEAD:0:12}
"

info "merging (no fast-forward to keep a clear sync commit)..."
if ! git merge --no-ff --no-edit -m "$MERGE_MSG" "$UPSTREAM_REMOTE/$UPSTREAM_BRANCH"; then
  fail "merge produced conflicts. Aborting cleanly."
  printf "\n%sConflicting paths:%s\n" "$C_BOLD" "$C_RESET"
  git diff --name-only --diff-filter=U || true
  git merge --abort
  warn "tree restored. Open a sub-issue per conflict scope (back / front / infra / cross-cutting)."
  exit 4
fi
ok "merge complete."

# --- push (optional) ---------------------------------------------------------
if [ "$PUSH" = 1 ]; then
  info "pushing to $ORIGIN_REMOTE/$LOCAL_BRANCH..."
  git push "$ORIGIN_REMOTE" "$LOCAL_BRANCH"
  ok "pushed."
else
  warn "merged locally. Not pushed. To publish: git push $ORIGIN_REMOTE $LOCAL_BRANCH"
fi

ok "sync done."
