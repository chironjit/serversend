#!/usr/bin/env bash
# AppImage packaging uses the existing linuxdeploy tool. Runtime GTK/WebKit
# stay on the host: bundling only their shared libraries breaks WebKit's
# subprocess/resource discovery and the host graphics drivers.
set -euo pipefail
: "${APP_NAME:?}" "${APP_BINARY:?}" "${ICON_PATH:?}" "${DESKTOP_FILE:?}"
APP_BINARY="$(realpath "$APP_BINARY")"
output="${APPIMAGE_OUTPUT:-$PWD/$APP_NAME.AppImage}"
work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT
case "$(uname -m)" in
  x86_64)
    tool_arch=x86_64
    digest=df908b19f1ed1cdafa79508d415aa12d6c56ce4d56d4149aa8c09286718706e5
    ;;
  aarch64|arm64)
    tool_arch=aarch64
    digest=f38e4d7cebf20d8073ec4e00e0a167ac146cd8585fe2f8fe82df27d74f2bd8e1
    ;;
  *) echo 'Unsupported AppImage architecture' >&2; exit 1 ;;
esac
appdir="$work/$APP_NAME.AppDir"
mkdir -p "$appdir/usr/bin"
cp "$APP_BINARY" "$appdir/usr/bin/$APP_NAME"
cp "$ICON_PATH" "$appdir/$APP_NAME.png"
ln -s "usr/bin/$APP_NAME" "$appdir/AppRun"
# Reuse the scaffold desktop entry with the actual executable/icon name.
sed -e "s/^Name=.*/Name=ServerSend/" -e "s/^Exec=.*/Exec=$APP_NAME/" \
  -e "s/^Icon=.*/Icon=$APP_NAME/" "$DESKTOP_FILE" > "$appdir/$APP_NAME.desktop"
cd "$work"
curl --fail --location --retry 3 --output linuxdeploy.AppImage \
  "https://github.com/linuxdeploy/linuxdeploy/releases/download/continuous/linuxdeploy-$tool_arch.AppImage"
# Fail if upstream's rolling artifact changes; review and update this pin.
printf '%s  linuxdeploy.AppImage\n' "$digest" | sha256sum --check -
chmod +x linuxdeploy.AppImage
# Extracting the tool avoids requiring FUSE on hosted runners.
./linuxdeploy.AppImage --appimage-extract >/dev/null
# Use linuxdeploy's bundled packager directly. Its dependency deployment pass
# rewrites ELF segments and can make Go build metadata unreadable. Host runtime
# libraries need no deployment, so preserve the compiled executable verbatim.
ARCH="$tool_arch" APPIMAGE_EXTRACT_AND_RUN=1 \
  ./squashfs-root/plugins/linuxdeploy-plugin-appimage/usr/bin/appimagetool "$appdir" "$output"
chmod +x "$output"
# Verify packaging preserves the executable bytes and Go build metadata.
mkdir verify
cd verify
"$output" --appimage-extract >/dev/null
test -x "squashfs-root/usr/bin/$APP_NAME"
cmp "$APP_BINARY" "squashfs-root/usr/bin/$APP_NAME"
go version -m "squashfs-root/usr/bin/$APP_NAME"
