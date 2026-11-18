#!/usr/bin/env bash
# CI only: install Linux desktop build prerequisites, reusing .debs cached in $1.
set -euo pipefail
cache="${1:?usage: ci-linux-desktop-deps.sh <cache-dir>}"
packages=(build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev libwayland-dev)

mkdir -p "$cache"
sudo rm -f /var/lib/man-db/auto-update # skip the slow man-db trigger
sudo find "$cache" -name '*.deb' -exec cp -t /var/cache/apt/archives/ {} +
sudo apt-get update
sudo apt-get install -y --no-install-recommends -o APT::Keep-Downloaded-Packages=true "${packages[@]}"

# Keep only the current package set for the next run.
find "$cache" -name '*.deb' -delete
find /var/cache/apt/archives -maxdepth 1 -name '*.deb' -exec cp -t "$cache" {} +
echo "cached $(find "$cache" -name '*.deb' | wc -l) packages"
