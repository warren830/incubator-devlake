#!/bin/sh
# Installs Kiro commit attribution into a repository.
#
# Usage:
#   ./install.sh                 install into the current repository
#   ./install.sh /path/to/repo   install into another repository
#   ./install.sh --check         report status without changing anything
#   ./install.sh --uninstall     remove what this script installed
#
# The hook always goes into .git/hooks/, and core.hooksPath is never touched.
# That needs saying because it looks wrong at first: on an Amazon laptop
# git-defender sets core.hooksPath at system scope, which by git's own rules
# means .git/hooks is ignored. It turns out git-defender invokes the local
# .git/hooks/prepare-commit-msg itself - verified by observation, and its error
# text ("Your local prepare-commit-msg hook failed") says as much.
#
# Redirecting core.hooksPath to work around it would be worse than unnecessary:
# git-defender reports the event ("User had a non Code Defender hooks path value
# set"), so the workaround trips a security signal while solving nothing.
#
# husky is the other common case. It also sets core.hooksPath, and its own hooks
# live in that directory - but husky's shims call .git/hooks too, so the same
# placement works.
set -eu

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
MODE=install
TARGET_REPO=.
MARKER='# kiro-attribution'

for arg in "$@"; do
  case "$arg" in
    --check)     MODE=check ;;
    --uninstall) MODE=uninstall ;;
    --help|-h)   sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    -*)          echo "unknown option: $arg" >&2; exit 2 ;;
    *)           TARGET_REPO=$arg ;;
  esac
done

cd "$TARGET_REPO" 2>/dev/null || { echo "not a directory: $TARGET_REPO" >&2; exit 1; }
# --absolute-git-dir, not --git-dir: the latter returns a path relative to the
# current directory (".git" at the repo root), and the verification step below
# cd's elsewhere. Every path derived from a relative GIT_DIR would then resolve
# against the wrong directory - silently, since sed on a missing file just
# produces an empty hook that chmod happily marks executable.
GIT_DIR=$(git rev-parse --absolute-git-dir 2>/dev/null) || {
  echo "not a git repository: $TARGET_REPO" >&2; exit 1
}
REPO_ROOT=$(git rev-parse --show-toplevel)
HOOK_DIR="$GIT_DIR/hooks"
HOOK_FILE="$HOOK_DIR/prepare-commit-msg"
SESSION_FILE="$GIT_DIR/kiro-sessions"

# ---------------------------------------------------------------- check --------

if [ "$MODE" = check ]; then
  echo "repository:  $REPO_ROOT"

  HP=$(git config --get core.hooksPath 2>/dev/null || true)
  if [ -n "$HP" ]; then
    echo "hooksPath:   $HP"
    echo "             (set by other tooling; .git/hooks is still used, see header)"
  else
    echo "hooksPath:   unset"
  fi

  if [ -f "$HOOK_FILE" ] && grep -q "$MARKER" "$HOOK_FILE" 2>/dev/null; then
    if [ -x "$HOOK_FILE" ]; then
      echo "commit hook: installed"
    else
      echo "commit hook: installed but NOT EXECUTABLE - git will skip it"
    fi
  elif [ -f "$HOOK_FILE" ]; then
    echo "commit hook: a different prepare-commit-msg is present (not ours)"
  else
    echo "commit hook: not installed"
  fi

  KIRO_HOOK="$REPO_ROOT/.kiro/hooks/kiro-session-record.kiro.hook"
  if [ -f "$KIRO_HOOK" ]; then
    echo "kiro hook:   registered"
  else
    echo "kiro hook:   not registered"
  fi

  if [ -f "$SESSION_FILE" ]; then
    echo "session log: $(wc -l < "$SESSION_FILE" | tr -d ' ') entries"
  else
    echo "session log: none yet (the Kiro hook has not fired in this repo)"
  fi

  # Coverage decides whether attribution can support a conclusion at all.
  # Unknown coverage means unusable data, not partial data: a commit with no
  # trailer is indistinguishable between "no AI was used" and "the hook was
  # never installed on that machine".
  TOTAL=$(git rev-list --count HEAD 2>/dev/null || echo 0)
  TAGGED=$(git log --all --grep='^Kiro-Session-Id:' --oneline 2>/dev/null | wc -l | tr -d ' ')
  echo "commits:     $TOTAL total, $TAGGED with a Kiro-Session-Id trailer"
  exit 0
fi

# ------------------------------------------------------------ uninstall --------

if [ "$MODE" = uninstall ]; then
  if [ -f "$HOOK_FILE" ] && grep -q "$MARKER" "$HOOK_FILE" 2>/dev/null; then
    if [ -f "$HOOK_FILE.kiro-backup" ]; then
      mv "$HOOK_FILE.kiro-backup" "$HOOK_FILE"
      echo "restored the pre-existing prepare-commit-msg"
    else
      rm -f "$HOOK_FILE"
      echo "removed $HOOK_FILE"
    fi
  else
    echo "no hook installed by this script"
  fi
  rm -f "$REPO_ROOT/.kiro/hooks/kiro-session-record.kiro.hook" 2>/dev/null || true
  echo "note: $SESSION_FILE is left in place; delete it manually if unwanted"
  exit 0
fi

# -------------------------------------------------------------- install --------

mkdir -p "$HOOK_DIR"

# Chain to an unrelated hook rather than replacing it: silently dropping another
# team's hook would break their workflow with nothing to point at.
CHAIN_TO=""
if [ -f "$HOOK_FILE" ] && ! grep -q "$MARKER" "$HOOK_FILE" 2>/dev/null; then
  mv "$HOOK_FILE" "$HOOK_FILE.kiro-backup"
  CHAIN_TO="$HOOK_FILE.kiro-backup"
  echo "kept the existing prepare-commit-msg as prepare-commit-msg.kiro-backup"
fi

{
  printf '#!/bin/sh\n'
  printf '%s - appends a Kiro-Session-Id trailer.\n' "$MARKER"
  printf '# Installed by tools/kiro-attribution/install.sh; safe to remove.\n'
  printf 'set -eu\n\n'
  if [ -n "$CHAIN_TO" ]; then
    printf '# Run the pre-existing hook first. Its failure must still abort the\n'
    printf '# commit, hence propagating the exit code.\n'
    printf 'if [ -x "%s" ]; then "%s" "$@" || exit $?; fi\n\n' "$CHAIN_TO" "$CHAIN_TO"
  fi
  # Inlined rather than sourced: a developer's checkout of this repository can
  # move or be deleted, and a hook pointing at a missing file would break every
  # commit in that repo.
  sed '1d' "$SCRIPT_DIR/prepare-commit-msg"
} > "$HOOK_FILE"

chmod +x "$HOOK_FILE"
echo "installed $HOOK_FILE"
[ -n "$CHAIN_TO" ] && echo "  chaining to $CHAIN_TO"

# Register the Kiro-side recorder for this workspace.
if [ -d "$REPO_ROOT/.kiro" ]; then
  mkdir -p "$REPO_ROOT/.kiro/hooks"
  cat > "$REPO_ROOT/.kiro/hooks/kiro-session-record.kiro.hook" <<EOF
{
  "version": "1.0.0",
  "enabled": true,
  "name": "kiro-session-record",
  "description": "Records the Kiro session id for commit attribution. Installed by tools/kiro-attribution/install.sh.",
  "when": { "type": "agentStop" },
  "then": {
    "type": "runCommand",
    "command": "$SCRIPT_DIR/kiro-session-record.sh"
  }
}
EOF
  echo "registered the Kiro stop hook in $REPO_ROOT/.kiro/hooks"
else
  echo "no .kiro/ directory here, so the Kiro-side hook was not registered."
  echo "  Add one with when.type = agentStop running:"
  echo "    $SCRIPT_DIR/kiro-session-record.sh"
fi

# ---------------------------------------------------------------- verify -------
# Proved rather than assumed. Every failure mode in this setup is silent: a
# missing executable bit, a timezone mismatch between the recorder and the hook,
# an off-by-one at the commit boundary. All three produce "no trailer, no
# error", which is indistinguishable from "no AI was used".
echo
echo "verifying in a scratch repository..."
VERIFY_DIR=$(mktemp -d)
trap 'rm -rf "$VERIFY_DIR"' EXIT
(
  cd "$VERIFY_DIR"
  git init -q
  git config user.email verify@example.com
  git config user.name verify
  mkdir -p .git/hooks
  # Point core.hooksPath at this scratch repo's own directory. Repository scope
  # beats system scope, so any org-wide tooling (git-defender) stays out of the
  # way while our hook still runs.
  #
  # --no-verify would be the obvious alternative and is wrong twice over: it
  # skips every hook including the one under test, and on a managed laptop it
  # records a security-scan bypass.
  git config core.hooksPath .git/hooks
  # Drop the chain line: the pre-existing hook belongs to the real repository and
  # may well refuse to run here.
  sed '/kiro-backup/d' "$HOOK_FILE" > .git/hooks/prepare-commit-msg
  chmod +x .git/hooks/prepare-commit-msg

  echo x > x && git add . && git commit -q -m baseline
  sleep 1
  printf '%s\tverify-session-id\t%s\n' "$(date -u +%s)" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    >> .git/kiro-sessions
  echo y > y && git add . && git commit -q -m "with session"

  if ! git log -1 --format=%B | grep -q 'Kiro-Session-Id: verify-session-id'; then
    echo "  FAILED: no trailer written. Do not rely on this install." >&2
    exit 1
  fi
  echo "  a trailer is written when a session was recorded"

  echo z > z && git add . && git commit -q -m "no new session"
  if git log -1 --format=%B | grep -q 'Kiro-Session-Id'; then
    echo "  FAILED: a commit with no new session inherited a trailer." >&2
    exit 1
  fi
  echo "  no trailer when no new session was recorded"
)
echo
echo "done. Check status any time with:"
echo "  $SCRIPT_DIR/install.sh --check"
