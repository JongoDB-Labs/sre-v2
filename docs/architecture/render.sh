#!/usr/bin/env bash
# Render every view *.d2 -> rendered/<name>.svg and .png
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p rendered
shopt -s nullglob
for f in [0-9][0-9]-*.d2; do
  name="${f%.d2}"
  echo "rendering $f"
  d2 --theme 200 --dark-theme 200 "$f" "rendered/${name}.svg"
  d2 --theme 200 "$f" "rendered/${name}.png"
done
echo "done -> rendered/"
