#!/usr/bin/env bash
# Stamps a release version into the build assets, so binaries and installers
# report the tagged version instead of the placeholder in the repo. CI runs it
# before packaging on tag builds; the changes are not meant to be committed.
#
# Usage: hack/set-version.sh [version]    (default: $RELEASE_VERSION, e.g. v1.2.0)
set -euo pipefail
cd "$(dirname "$0")/.."

raw=${1:-${RELEASE_VERSION:-}}
version=${raw#v}
if ! [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$ ]]; then
  echo "'$raw' is not a semantic version; using 0.0.0-dev (e.g. for a manual dry run)."
  version=0.0.0-dev
fi
# Windows and macOS version fields accept only numbers, so drop any
# pre-release/build suffix there (1.2.0-rc.1 -> 1.2.0).
numeric=${version%%[-+]*}
export V="$version" N="$numeric"

# stamp FILE PERL_SUBSTITUTION EXPECTED_TEXT
stamp() {
  perl -0pi -e "$2" "$1"
  if ! grep -qF -- "$3" "$1"; then
    echo "::error::Failed to stamp version into $1"
    exit 1
  fi
  echo "Stamped $1"
}

stamp build/config.yml             's/^(  version: )"[^"]*"/$1"$ENV{V}"/m'                                        "version: \"$version\""
stamp build/linux/nfpm/nfpm.yaml   's/^(version: )"[^"]*"/$1"$ENV{V}"/m'                                          "version: \"$version\""
stamp build/darwin/Info.plist      's#(<key>CFBundle(?:ShortVersionString|Version)</key>\s*<string>)[^<]*#$1$ENV{N}#g' "<string>$numeric</string>"
stamp build/windows/info.json      's/("(?:file_version|ProductVersion)":\s*")[^"]*/$1$ENV{N}/g'                 "\"ProductVersion\": \"$numeric\""
stamp build/windows/nsis/wails_tools.nsh 's/(!define INFO_PRODUCTVERSION ")[^"]*/$1$ENV{N}/'                      "INFO_PRODUCTVERSION \"$numeric\""
