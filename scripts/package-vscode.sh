#!/bin/sh
# Package extensions/vscode into GUI + TUI VSIX. Not part of make check.
# Usage: scripts/package-vscode.sh [version]
#   version: strip leading v from $1 or $YMZ_VERSION; else package.json.
# Output: extensions/vscode/ymz-vscode_{version}.vsix
#         extensions/vscode/ymz-vscode-tui_{version}.vsix
#         plus dist/vscode/ copies when that dir is writable (release as root).
# Stamps package.json only for the pack, then restores it (no git bump).
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
EXT="$ROOT/extensions/vscode"

if ! command -v node >/dev/null 2>&1; then
  for n in /home/yyze/.nvm/versions/node/*/bin/node /usr/local/bin/node /usr/bin/node; do
    if [ -x "$n" ]; then
      PATH="$(dirname "$n"):$PATH"
      export PATH
      break
    fi
  done
fi

if ! command -v node >/dev/null 2>&1 || ! command -v npm >/dev/null 2>&1; then
  echo "package-vscode: Node.js + npm required" >&2
  exit 1
fi

VERSION_ARG="${1:-${YMZ_VERSION:-}}"
if [ -n "$VERSION_ARG" ]; then
  VERSION="${VERSION_ARG#v}"
else
  VERSION="$(node -p "require('$EXT/package.json').version")"
fi

cd "$EXT"
if [ ! -d node_modules ]; then
  npm ci
fi
npm run check-types
npm test
npm run package

OUT_GUI="$EXT/ymz-vscode_${VERSION}.vsix"
OUT_TUI="$EXT/ymz-vscode-tui_${VERSION}.vsix"
rm -f "$EXT"/ymz-vscode-*.vsix "$EXT"/ymz-vscode_*.vsix

ORIG_JSON="$(mktemp)"
cp package.json "$ORIG_JSON"
trap 'cp "$ORIG_JSON" package.json; rm -f "$ORIG_JSON"' EXIT INT HUP TERM

node -e "
const fs = require('fs');
const p = JSON.parse(fs.readFileSync('package.json', 'utf8'));
p.version = process.argv[1];
fs.writeFileSync('package.json', JSON.stringify(p, null, 2) + '\n');
" "$VERSION"
npx --no-install vsce package --no-dependencies -o "$OUT_GUI"

node -e "
const fs = require('fs');
const p = JSON.parse(fs.readFileSync('package-tui.json', 'utf8'));
p.version = process.argv[1];
fs.writeFileSync('package.json', JSON.stringify(p, null, 2) + '\n');
" "$VERSION"
npx --no-install vsce package --no-dependencies --ignoreFile .vscodeignore-tui -o "$OUT_TUI"

cp "$ORIG_JSON" package.json

if mkdir -p "$ROOT/dist/vscode" 2>/dev/null; then
  cp -f "$OUT_GUI" "$ROOT/dist/vscode/ymz-vscode_${VERSION}.vsix"
  cp -f "$OUT_TUI" "$ROOT/dist/vscode/ymz-vscode-tui_${VERSION}.vsix"
  echo "Copied $ROOT/dist/vscode/ymz-vscode_${VERSION}.vsix"
  echo "Copied $ROOT/dist/vscode/ymz-vscode-tui_${VERSION}.vsix"
fi

echo "Wrote $OUT_GUI"
echo "Wrote $OUT_TUI"
echo "Install GUI: code --install-extension $OUT_GUI"
echo "Install TUI: code --install-extension $OUT_TUI"
echo "Do not install both (shortcut clash)."
