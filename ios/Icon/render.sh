#!/bin/sh
# Renders the app icon SVGs in this directory into the asset catalog's
# 1024 px PNGs. Needs rsvg-convert (brew install librsvg). The default
# icon is flattened to drop its alpha channel, which the App Store
# rejects; the dark and tinted variants keep their transparency.
set -eu
cd "$(dirname "$0")"
out=../App/Assets.xcassets/AppIcon.appiconset
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for v in default dark tinted; do
  rsvg-convert -w 1024 -h 1024 "icon-$v.svg" -o "$tmp/$v.png"
done
# PNG -> BMP -> PNG round trip through sips drops the alpha channel losslessly.
sips -s format bmp "$tmp/default.png" --out "$tmp/default.bmp" >/dev/null
sips -s format png "$tmp/default.bmp" --out "$out/AppIcon.png" >/dev/null
cp "$tmp/dark.png" "$out/AppIcon-Dark.png"
cp "$tmp/tinted.png" "$out/AppIcon-Tinted.png"
echo "wrote $out"
