#!/bin/zsh
# a commented-out `claude -p "old"` must not count
set -e
cd "$HOME"
/opt/homebrew/bin/claude -p "/changelog-update"
