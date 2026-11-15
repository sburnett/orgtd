#!/bin/sh
# Builds ios/Frameworks/Mobilecore.xcframework from ./mobilecore with
# gomobile, for the iOS app (see ios/README.md). Needs Xcode. gomobile and
# gobind are pinned as tools in go.mod, so nothing else needs installing.
set -eu
cd "$(dirname "$0")/.."
bin=$(mktemp -d)
trap 'rm -rf "$bin"' EXIT
go build -o "$bin/gomobile" golang.org/x/mobile/cmd/gomobile
go build -o "$bin/gobind" golang.org/x/mobile/cmd/gobind
PATH="$bin:$PATH"
mkdir -p ios/Frameworks
rm -rf ios/Frameworks/Mobilecore.xcframework
gomobile bind \
  -target=ios,iossimulator \
  -iosversion=17.0 \
  -trimpath -ldflags=-s \
  -o ios/Frameworks/Mobilecore.xcframework \
  ./mobilecore
echo "built ios/Frameworks/Mobilecore.xcframework"
