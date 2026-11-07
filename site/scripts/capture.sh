#!/usr/bin/env bash
# Screenshot rig for the website: real safina against the demo cluster,
# rendered offscreen. Nothing is drawn on your display.
#
# safina runs inside a private tmux server (so keys can be scripted). Shots
# read the exact cell grid from tmux and render it with xterm.js in headless
# Chromium at 2x (snap.mjs).
#
#   capture.sh start            build safina and start it (backs up ~/.safina/config.yaml)
#   capture.sh keys <k>...      send keys (tmux send-keys syntax)
#   capture.sh text             print the current screen as plain text
#   capture.sh shot <name>      render src/assets/shots/<name>.png
#   capture.sh stop             stop safina and restore the safina config
#
# Needs: the demo cluster (demo-cluster.sh), tmux, chromium, node, go.

set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
STATE="$HERE/../.demo"
SOCK=safina-capture
COLS="${COLS:-140}"
ROWS="${ROWS:-40}"
KCFG="${DEMO_KUBECONFIG:-$STATE/kubeconfig}"

tm() { tmux -L "$SOCK" -f /dev/null "$@"; }

case "${1:-}" in
start)
	mkdir -p "$STATE"
	[[ -f "$KCFG" ]] || { echo "no demo kubeconfig at $KCFG; run demo-cluster.sh first" >&2; exit 1; }
	if [[ -f "$HOME/.safina/config.yaml" && ! -f "$STATE/config.yaml.bak" ]]; then
		cp "$HOME/.safina/config.yaml" "$STATE/config.yaml.bak"
	fi
	# Reuse the last good build unless REBUILD=1, so a half-finished change in
	# the Go tree never blocks a screenshot session.
	if [[ "${REBUILD:-0}" == 1 || ! -x "$STATE/safina" ]]; then
		(cd "$ROOT" && go build -o "$STATE/safina" ./cmd/safina)
	fi

	tm kill-server 2>/dev/null || true
	# TMUX unset + truecolor TERM so safina's colour detection picks 24-bit.
	tm new-session -d -s demo -x "$COLS" -y "$ROWS" \
		-e KUBECONFIG="$KCFG" -e EDITOR="$HERE/edit-fix.sh" -e VISUAL="$HERE/edit-fix.sh" \
		"env -u TMUX TERM=xterm-256color COLORTERM=truecolor $STATE/safina --kubeconfig $KCFG"
	tm set -g status off
	tm set -g window-size manual
	tm resize-window -t demo -x "$COLS" -y "$ROWS"
	sleep 1
	# No client is attached, so tell safina its terminal has focus; it pauses
	# refresh while blurred.
	tm send-keys -t demo -H 1b 5b 49
	echo "safina running in tmux -L $SOCK (${COLS}x${ROWS})"
	;;

keys)
	shift
	tm send-keys -t demo "$@"
	;;

text)
	tm capture-pane -p -t demo
	;;

shot)
	node "$HERE/snap.mjs" "${2:?shot name}"
	;;

stop)
	tm kill-server 2>/dev/null || true
	if [[ -f "$STATE/config.yaml.bak" ]]; then
		cp "$STATE/config.yaml.bak" "$HOME/.safina/config.yaml"
		rm "$STATE/config.yaml.bak"
		echo "restored ~/.safina/config.yaml"
	fi
	;;

*)
	sed -n '2,16p' "$0"
	exit 2
	;;
esac
