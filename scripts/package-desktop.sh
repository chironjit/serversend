#!/usr/bin/env bash
# CI-only native downloads using pinned dependencies; no Taskfile tidy/install hooks.
set -euo pipefail
version="${1:?Pass the release version}"
[[ "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || exit 1
root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root/apps/desktop"
export CGO_ENABLED=1 GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off
os="$(go env GOOS)"
arch="$(go env GOARCH)"
output="$root/dist"
mkdir -p "$output"
stage="$(mktemp -d)"
trap 'rm -rf -- "$stage"; rm -f -- release_windows_*.syso' EXIT
name="serversend-desktop-$version-$os-$arch"
case "$os" in
  linux)
    go build -mod=readonly -tags production -trimpath -ldflags='-s -w' -o "$stage/serversend" .
    tar -czf "$output/$name.tar.gz" -C "$stage" serversend
    ;;
  windows)
    node --input-type=module - "$version" "$stage/info.json" <<'JS'
import { readFileSync, writeFileSync } from 'node:fs';
const info = JSON.parse(readFileSync('build/windows/info.json', 'utf8'));
info.fixed.file_version = process.argv[2];
info.info['0409'].ProductVersion = process.argv[2];
info.info['0409'].ProductName = 'ServerSend';
info.info['0409'].FileDescription = 'ServerSend desktop';
writeFileSync(process.argv[3], JSON.stringify(info));
JS
    wails3 generate syso -arch "$arch" -icon build/windows/icon.ico -manifest build/windows/wails.exe.manifest -info "$stage/info.json" -out "release_windows_$arch.syso"
    go build -mod=readonly -tags production -trimpath -ldflags='-s -w -H windowsgui' -o "$stage/ServerSend.exe" .
    # PowerShell is included on the hosted Windows runner. Convert Git Bash paths.
    STAGE_WINDOWS="$(cygpath -w "$stage/ServerSend.exe")" OUTPUT_WINDOWS="$(cygpath -w "$output/$name.zip")" \
      powershell.exe -NoProfile -Command 'Compress-Archive -LiteralPath $env:STAGE_WINDOWS -DestinationPath $env:OUTPUT_WINDOWS -Force'
    ;;
  darwin)
    app="$stage/ServerSend.app/Contents"
    mkdir -p "$app/MacOS" "$app/Resources"
    go build -mod=readonly -tags production -trimpath -ldflags='-s -w' -o "$app/MacOS/desktop" .
    cp build/darwin/Info.plist "$app/Info.plist"
    cp build/darwin/icons.icns "$app/Resources/"
    if [[ -f build/darwin/Assets.car ]]; then cp build/darwin/Assets.car "$app/Resources/"; fi
    /usr/libexec/PlistBuddy -c "Set :CFBundleName ServerSend" "$app/Info.plist"
    /usr/libexec/PlistBuddy -c "Set :CFBundleVersion $version" "$app/Info.plist"
    /usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $version" "$app/Info.plist"
    xattr -cr "$stage/ServerSend.app"
    codesign --force --deep --sign - "$stage/ServerSend.app"
    codesign --verify --deep --strict "$stage/ServerSend.app"
    ditto -c -k --sequesterRsrc --keepParent "$stage/ServerSend.app" "$output/$name.zip"
    ;;
  *) echo "Unsupported release OS: $os" >&2; exit 1 ;;
esac
