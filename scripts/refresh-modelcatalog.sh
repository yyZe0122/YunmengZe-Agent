#!/bin/sh
# Refresh the embedded models.dev snapshot used as the offline default catalog.
set -eu
root="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
dest="$root/internal/modelcatalog/data/models.json"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
curl -fsSL -A 'YunmengZe/0' --max-time 60 -o "$tmp" https://models.dev/models.json
python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert isinstance(d, dict) and len(d)>0, "empty catalog"' "$tmp"
install -m 0644 "$tmp" "$dest"
echo "updated $dest ($(wc -c < "$dest") bytes)"
