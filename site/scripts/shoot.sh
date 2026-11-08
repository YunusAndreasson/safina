#!/usr/bin/env bash
# The whole shoot as one take: every still and every recording on the site,
# in page order, from one safina session against the demo cluster, so the
# clocks only move forward and the ages agree (README §Screenshots).
#
#   scripts/shoot.sh setup     reset the incident, build safina, open Workloads on ledger-db,
#                              start recording; then let it run WARMUP seconds (default 900)
#                              so the trend lines have history
#   scripts/shoot.sh tour      Drill in, Next failing, Logs (a crash caught live), Events, Hot pods, Metrics
#   scripts/shoot.sh chat      Logbook → the logs entry → c: chat reads the lines (live model)
#   scripts/shoot.sh fix       at COLS x FIX_ROWS (160x30): E, the diff, y, Rollout watch to 2/2 ready
#   scripts/shoot.sh logbook   at COLS x ROWS (160x37): the Logbook with the edit's whole story
#   scripts/shoot.sh wrap      stop, cut every scene into src/assets/casts, crops, font
#
# A re-take in the same recording replaces the earlier one (castcut takes the
# last "scene <name>"). Each scene is recorded between "scene <name>" and "end <name>" markers and
# ends on its still (shot right after), except fix, whose still is the diff
# it plays from. Needs what capture.sh needs.
set -euo pipefail
cd "$(dirname "$0")/.."
C=scripts/capture.sh
CASTS=src/assets/casts
# The terminal (capture.sh's defaults); the diff is shot shorter, so the
# edit modal fits its diff.
COLS="${COLS:-160}" ROWS="${ROWS:-37}" FIX_ROWS="${FIX_ROWS:-30}"
export COLS ROWS

# Wait until the screen shows a line matching the regex.
wait_for() {
	local re="$1" secs="${2:-30}" i=0
	until $C text | grep -qE -- "$re"; do
		sleep 0.25
		i=$((i + 1))
		if ((i > secs * 4)); then
			echo "timed out waiting for: $re" >&2
			$C text >&2
			return 1
		fi
	done
}
# Type like a person, one key at a time.
type_slowly() {
	local s="$1" k
	for ((k = 0; k < ${#s}; k++)); do
		$C keys -l "${s:k:1}"
		sleep 0.09
	done
}
# Move the cursor until the inspector's box carries <name> (down, then up).
cursor_to() {
	local name="$1" key i
	for key in Down Up; do
		for i in $(seq 30); do
			$C text | grep -q "╭─ $name ─" && return 0
			$C keys "$key"
			sleep 0.25
		done
	done
	echo "no row for $name" >&2
	return 1
}
# Back out to the workload list.
to_workloads() {
	local i
	for i in 1 2 3 4; do
		$C text | sed -n 1p | grep -qE "Workloads \(25\) *$|Workloads \(25\) +Nodes" && return 0
		$C keys Escape
		sleep 0.8
	done
}
k() { KUBECONFIG=.demo/kubeconfig kubectl "$@"; }
# The ledger-api pod that crash-loops back to life next, and in how many
# seconds: its last exit plus Kubernetes' back-off (10s doubling, 5m cap).
next_crash() {
	k -n ledger get pods -l app=ledger-api -o json | node -e '
		const pods = JSON.parse(require("fs").readFileSync(0, "utf8")).items;
		let best;
		for (const p of pods) {
			const cs = p.status.containerStatuses?.[0];
			const fin = Date.parse(cs?.lastState?.terminated?.finishedAt ?? "") / 1000;
			if (!cs || !fin) continue;
			const at = fin + Math.min(10 * 2 ** Math.max(0, cs.restartCount - 1), 300);
			if (!best || at < best.at) best = { name: p.metadata.name, at };
		}
		if (best) console.log(best.name, Math.max(0, Math.round(best.at - Date.now() / 1000)));'
}
state_of() { k -n ledger get pod "$1" -o jsonpath='{.status.containerStatuses[0].state}'; }
scene() { $C mark "scene $1"; }
end() {
	sleep "${2:-1}"
	$C mark "end $1"
}

case "${1:-}" in
setup)
	[[ "${RESET:-1}" == 1 ]] && scripts/demo-cluster.sh
	REBUILD="${REBUILD:-1}" $C start
	wait_for "pick a cluster"
	# The clusters are listed by name; eu-north-1 is the second.
	$C keys Down
	sleep 0.4
	$C keys Enter
	wait_for "Namespaces"
	$C keys Enter
	wait_for "Resources"
	$C keys Enter
	# The top bar, not the resource-type list (which names "Workloads (25)"
	# before the list itself opens).
	for _ in $(seq 120); do
		$C text | sed -n 1p | grep -qE "Workloads \(25\)" && break
		sleep 0.25
	done
	cursor_to ledger-db
	$C rec start shoot
	echo "recording; let safina sit ${WARMUP:-900}s on Workloads before 'tour'"
	;;

tour)
	scene drill # Enter into ledger-db, its pod, its container; Esc back up
	$C keys Enter
	wait_for "╭─ Pods"
	sleep 1.4
	$C keys Enter
	wait_for "╭─ Containers"
	sleep 1.6
	$C keys Escape
	sleep 1
	$C keys Escape
	wait_for "╭─ ledger-db ─"
	end drill 1.2
	$C shot workloads

	scene failing
	$C keys n
	wait_for "╭─ ledger-api ─"
	end failing 1.2
	$C shot failing

	# Into ledger-api's pods, onto the one that comes back next, and catch
	# its crash live: l the moment its container starts.
	$C keys Enter
	wait_for "╭─ Pods"
	read -r pod secs < <(next_crash)
	cursor_to "$pod"
	echo "waiting about ${secs}s for $pod to start" >&2
	until [[ "$(state_of "$pod")" == *waiting* ]]; do sleep 0.3; done
	until [[ "$(state_of "$pod")" == *running* ]]; do sleep 0.15; done
	scene logs
	$C keys l
	wait_for "panic:" 30
	end logs 1.6
	$C shot logs

	scene events # the pod's: the back-off
	$C keys e
	wait_for "[0-9]+ events" 15
	end events 1.6
	$C shot events

	scene hot
	$C keys h
	wait_for " #  name +namespace" 15
	end hot 2.4
	$C shot hot

	# The hot pod over time: settlement-worker's metrics, zoomed to the
	# last five minutes and back out to fifteen.
	to_workloads
	cursor_to settlement-worker
	scene metrics
	$C keys m
	wait_for "locally observed"
	sleep 1.8
	$C keys "["
	sleep 1.8
	$C keys "]"
	end metrics 2.2
	$C shot metrics
	;;

chat)
	# The live crash's logs, from the Logbook, handed to chat with a question.
	$C keys Escape # close the metrics pane, so the arrows move the list
	sleep 1
	to_workloads
	cursor_to ledger-api
	scene chat
	sleep 0.8
	$C keys :
	sleep 0.6
	type_slowly logbook
	sleep 0.5
	$C keys Enter
	wait_for "Logbook"
	sleep 1.2
	for _ in $(seq 20); do
		$C text | grep -qE "  │ 20[0-9-]+ [0-9:]+Z · logs · capture ended" && break
		$C keys Down
		sleep 0.35
	done
	sleep 0.8
	$C keys Enter
	wait_for "Logbook entry"
	sleep 1.2
	$C keys c
	sleep 1.4
	type_slowly " Why does ledger-api keep crashing?"
	sleep 0.8
	$C keys Enter
	# Done when the answer has stopped streaming for a few seconds.
	sleep 6
	prev=""
	for _ in $(seq 150); do
		cur="$($C text)"
		if [[ "$cur" == "$prev" ]] && ! grep -q "thinking…" <<<"$cur"; then break; fi
		prev="$cur"
		sleep 3
	done
	end chat 1
	$C shot chat
	;;

fix)
	$C keys Escape
	sleep 0.6
	to_workloads
	cursor_to ledger-api
	$C size "$COLS" "$FIX_ROWS"
	sleep 1.5
	scene fix
	$C keys E
	wait_for "apply edits to Deployment"
	sleep 1.2
	$C shot diff
	$C mark "still diff"
	sleep 1.2
	$C keys y
	# Rollout watch's banners run to "edit complete"; the row reaches 2/2
	# a moment later. Ends there.
	wait_for "edit complete" 180
	wait_for "ledger-api +ledger +2/2" 60
	sleep 0.3
	$C mark "end fix"
	;;

logbook)
	$C size "$COLS" "$ROWS"
	sleep 1.5
	scene history
	$C keys :
	sleep 0.5
	type_slowly logbook
	sleep 0.4
	$C keys Enter
	wait_for "Logbook"
	end history 1.6
	$C shot history
	;;

wrap)
	$C rec stop
	$C stop
	mkdir -p "$CASTS"
	raw="$(ls -t .demo/casts/*.cast | head -1)"
	for s in drill:workloads failing:failing logs:logs events:events hot:hot metrics:metrics chat:chat history:history; do
		node scripts/castcut.mjs "$raw" "$CASTS/${s#*:}.cast" --from "m:scene ${s%%:*}" --to "m:end ${s%%:*}"
	done
	# The fix plays from its still, the diff, through y to the banner and
	# 2/2, and holds there.
	node scripts/castcut.mjs "$raw" "$CASTS/fix.cast" --from "m:still diff" --to "m:end fix" --tail 2.5
	scripts/crops.sh
	scripts/termfont.sh
	# When each still was shot (the README and AGENTS.md cite the session).
	node -e '
		const [h, ...ev] = require("fs").readFileSync(process.argv[1], "utf8").trim().split("\n").map((l) => JSON.parse(l));
		for (const [t, k, d] of ev) if (k === "m" && /^(end|still) /.test(d))
			console.log(d.padEnd(16), new Date((h.timestamp + t) * 1000).toISOString().slice(11, 16), "UTC");
	' "$raw"
	;;

*)
	sed -n '2,20p' "$0"
	exit 2
	;;
esac
