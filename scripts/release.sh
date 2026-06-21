#!/bin/bash
set -e

usage() {
  cat <<EOF
Usage: $(basename "$0") <tag> [--force|-f]

Create an annotated tag and push it to trigger the Release workflow.

Arguments:
  tag         Tag name (e.g. v0.1.0)
  --force, -f Overwrite existing tag locally and remotely

Examples:
  $(basename "$0") v0.1.0
  $(basename "$0") v0.2.0 --force
EOF
  exit 0
}

TAG=$1
FORCE=false

if [ -z "$TAG" ]; then
  usage
fi

case "$2" in
  --force|-f) FORCE=true ;;
  --help|-h)  usage ;;
esac

if ! git rev-parse --git-dir > /dev/null 2>&1; then
  echo "Error: not in a git repository"
  exit 1
fi

if git tag -l "$TAG" | grep -q "^$TAG$"; then
  if [ "$FORCE" = false ]; then
    echo "Error: tag '$TAG' already exists. Use --force to overwrite."
    exit 1
  fi
  echo "Warning: tag '$TAG' exists locally, will overwrite."
fi

TMPFILE=$(mktemp)
cat > "$TMPFILE" <<EOF
# Release $TAG

Changes:

EOF

${EDITOR:-vi} "$TMPFILE"

NOTES=$(cat "$TMPFILE")
if [ -z "$NOTES" ]; then
  echo "Error: release notes cannot be empty"
  rm -f "$TMPFILE"
  exit 1
fi

echo ""
echo "=== Release Notes ==="
echo "$NOTES"
echo "====================="
echo ""

read -r -p "Create and push tag '$TAG'? [Y/n] " CONFIRM
if [ "$CONFIRM" = "n" ] || [ "$CONFIRM" = "N" ]; then
  echo "Cancelled."
  rm -f "$TMPFILE"
  exit 0
fi

if [ "$FORCE" = true ] && git tag -l "$TAG" | grep -q "^$TAG$"; then
  git tag -d "$TAG"
  git push origin ":refs/tags/$TAG" 2>/dev/null || true
fi

git tag -a "$TAG" -F "$TMPFILE"
git push origin "$TAG"

rm -f "$TMPFILE"
echo "Done! Tag '$TAG' pushed. View the build at:"
REPO_URL=$(git remote get-url origin | sed -E 's#^.*[/:]([^/]+/[^/]+)\.git$#\1#')
echo "  https://github.com/$REPO_URL/actions"
