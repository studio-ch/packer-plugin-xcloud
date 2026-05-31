#!/usr/bin/env bash
# install-clt.sh — installiert Xcode Command Line Tools headless.
set -euo pipefail

PLACEHOLDER="/tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress"
sudo touch "$PLACEHOLDER"
trap 'sudo rm -f "$PLACEHOLDER"' EXIT

CLT_LABEL="$(sudo softwareupdate -l \
    | grep -E '\*\s.*Command Line Tools' \
    | awk -F'Label: ' '/Label:/ {print $2}' \
    | sed 's/^ *//;s/ *$//' \
    | tail -n 1)"

echo "Installiere: $CLT_LABEL"
sudo softwareupdate -i "$CLT_LABEL" --verbose --agree-to-license

sudo xcode-select -s /Library/Developer/CommandLineTools
