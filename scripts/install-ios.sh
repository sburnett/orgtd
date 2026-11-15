#!/bin/sh
# Builds the iOS app and installs it on a paired iPhone, then launches it.
#
#   scripts/install-ios.sh [device]
#
# device is a name ("Sam's iPhone") or identifier from
# `xcrun devicectl list devices`; it can be left out when exactly one
# physical iPhone is paired. Needs ios/Config/Signing.local.xcconfig (see
# ios/README.md) and your Apple ID signed in under Xcode → Settings →
# Accounts, so Xcode can create and renew the provisioning profile.
#
# The Go framework is rebuilt first whenever a Go file or go.mod/go.sum is
# newer than it. Set FORCE_CORE=1 to rebuild it regardless.
set -eu
cd "$(dirname "$0")/.."
root=$(pwd)
want=${1:-}

signing=ios/Config/Signing.local.xcconfig
if ! grep -Eq '^[[:space:]]*DEVELOPMENT_TEAM[[:space:]]*=[[:space:]]*[A-Z0-9]{10}' "$signing" 2>/dev/null; then
  echo "error: set DEVELOPMENT_TEAM in $signing (copy $signing.example; see ios/README.md)" >&2
  exit 1
fi

# --- Go core ---------------------------------------------------------------
framework=ios/Frameworks/Mobilecore.xcframework
stamp=$framework/Info.plist
if [ "${FORCE_CORE:-}" = 1 ] || [ ! -f "$stamp" ] ||
   [ -n "$(find mobilecore internal go.mod go.sum -newer "$stamp" \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \) ! -name '*_test.go' -print -quit)" ]; then
  echo "==> Building Go framework"
  ./scripts/build-mobilecore.sh
else
  echo "==> Go framework is up to date"
fi

# --- Pick the device ---------------------------------------------------------
devices=$(mktemp)
trap 'rm -f "$devices"' EXIT
xcrun devicectl list devices --quiet --json-output "$devices" >/dev/null
picked=$(python3 - "$devices" "$want" <<'EOF'
import json, sys
path, want = sys.argv[1], sys.argv[2]
devs = [d for d in json.load(open(path))["result"]["devices"]
        if d["hardwareProperties"].get("reality") == "physical"
        and d["hardwareProperties"].get("platform") == "iOS"]
def norm(s): return s.replace("’", "'").casefold()
if want:
    devs = [d for d in devs if norm(want) in (norm(d["deviceProperties"].get("name", "")), d["identifier"].casefold(), d["hardwareProperties"].get("udid", "").casefold())]
if len(devs) != 1:
    names = ", ".join(repr(d["deviceProperties"].get("name")) for d in devs) or "none"
    sys.exit(f"error: need exactly one matching physical iPhone; found {names}. Pass its name or identifier.")
d = devs[0]
print(d["identifier"], d["hardwareProperties"]["udid"], d["deviceProperties"].get("name", ""), sep="\t")
EOF
)
device_id=$(printf '%s' "$picked" | cut -f1)
udid=$(printf '%s' "$picked" | cut -f2)
name=$(printf '%s' "$picked" | cut -f3)
echo "==> Device: $name ($udid)"

# --- Build -------------------------------------------------------------------
derived=$root/ios/build
echo "==> Building app (this signs with your team and may update the provisioning profile)"
xcodebuild \
  -project ios/orgtd.xcodeproj \
  -scheme orgtd \
  -configuration Release \
  -destination "id=$udid" \
  -derivedDataPath "$derived" \
  -allowProvisioningUpdates \
  -quiet \
  build
app=$derived/Build/Products/Release-iphoneos/orgtd.app
bundle_id=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$app/Info.plist")

# --- Install and launch ------------------------------------------------------
echo "==> Installing $bundle_id"
xcrun devicectl device install app --device "$device_id" "$app" >/dev/null

echo "==> Launching"
if ! xcrun devicectl device process launch --device "$device_id" "$bundle_id" >/dev/null 2>&1; then
  cat >&2 <<EOF
Installed, but it couldn't be launched. The first time, iOS has to trust
your developer certificate: on the phone, open Settings → General → VPN &
Device Management, tap your Apple ID, and tap Trust. (Developer Mode must
also be on: Settings → Privacy & Security → Developer Mode.)
EOF
  exit 0
fi
echo "==> Done. With a free team, run this again within 7 days to keep the app launching."
