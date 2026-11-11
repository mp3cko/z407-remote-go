#!/bin/sh
# Wraps the macOS program in "Z407 Remote.app", which is what gives it an
# icon in Finder: a plain program file cannot carry one.
#
#   packaging/macos-app.sh <z407 binary> <output directory> [version]
#
# The version may be a tag such as v1.2.3-rc.1; the app gets 1.2.3.
set -eu

bin=$1
out=$2
version=${3:-0.0.0}
version=${version#v}
version=${version%%-*}

here=$(cd "$(dirname "$0")" && pwd)
app="$out/Z407 Remote.app"

rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$bin" "$app/Contents/MacOS/z407"
chmod 755 "$app/Contents/MacOS/z407"
cp "$here/macos/z407.icns" "$app/Contents/Resources/z407.icns"
sed "s/@VERSION@/$version/g" "$here/macos/Info.plist" > "$app/Contents/Info.plist"

# An ad-hoc signature seals the app; Apple silicon refuses to run unsigned code.
if command -v codesign >/dev/null 2>&1; then
	codesign --force --deep --sign - "$app"
fi
echo "$app"
