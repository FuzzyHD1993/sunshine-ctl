#!/usr/bin/env bash
# Установка sunshine-ctl в ~/.local/bin + systemd user unit.
set -euo pipefail
cd "$(dirname "$0")"

command -v go >/dev/null || { echo "нужен Go 1.21+"; exit 1; }

echo "==> сборка"
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o sunshine-ctl .
install -Dm755 sunshine-ctl "$HOME/.local/bin/sunshine-ctl"

echo "==> systemd"
install -Dm644 systemd/sunshine-ctl.service "$HOME/.config/systemd/user/sunshine-ctl.service"
systemctl --user daemon-reload
systemctl --user enable --now sunshine-ctl.service

echo "==> готово"
echo "bin:  $HOME/.local/bin/sunshine-ctl"
echo "unit: sunshine-ctl.service"
echo
echo "Дальше настрой виртуальный выход (см. README, раздел EDID) и запусти:"
echo "  sunshine-ctl status"
