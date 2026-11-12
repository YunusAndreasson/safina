# Using safina

## Usage

```bash
# Use default kubeconfig
safina

# Specify kubeconfig path
safina --kubeconfig=/path/to/kubeconfig

# Start with a specific context
safina --context=my-cluster

# Start with a specific namespace
safina --namespace=default
```

## Features

- Browse multiple Kubernetes clusters/contexts from kubeconfig
- Switch contexts without restarting
- List namespaces per cluster
- View pods with color-coded status (Green=Running, Yellow=Pending, Red=Failed)
- Delete pods with confirmation
- Scale deployments with replica count input
- Rollback deployments to previous revision
- Stream pod logs in follow mode with scrollback; choose the pod/container and inspect previous logs
- Track scale and restart requests through controller readiness
- The top bar says where you are and how the open resource is doing, and carries confirmations, operations in flight, pending proposals and data age (`paused`, `↻ 12s`) so the page never shifts
- Persistent errors and notification history (`Ctrl+N`); dismiss the error row with `Ctrl+D`
- Compact resource search (`:`); `Ctrl+S` explicitly loads the selected namespace’s supported resource kinds
- Auto-refresh pod/deployment lists every 5 seconds
- Asks which cluster on every launch, so a muscle-memory keypress never lands on the wrong one
- Help screen with all keybindings

Search starts with already-loaded resources. Inside a namespace, `Ctrl+S` expands it to workloads, their pods, ConfigMaps, Secrets, Services, Ingresses, and PVCs. Partial failures are labeled. `Tab` in the palette shows resource actions. In logs, `o` switches targets and `p` opens previous-container logs when available.

Log scrollback keeps up to 10,000 recent lines and 8 MiB of raw text; individual
records retain up to 256 KiB. Older data is discarded in batches, and the tab
rule reports truncation. Copy uses the retained output. Switching log targets
releases the previous buffer.

Chat scrollback keeps up to 200 turns and 4 MiB, with a 1 MiB display limit per
response. Omitted turns and truncated responses are labeled. This limits the
terminal's scrollback, not the CLI's resumable conversation.

Notification history keeps the latest 50 messages for the session. `Enter` opens the full message; `r` retries supported failed reads without replaying a mutation. Scale/restart monitoring checks controller status for up to five minutes and distinguishes acceptance, readiness, and monitoring failures.

## Resource charts

Press `m` on a workload, pod, or node to inspect CPU and memory trends.
Charts use the existing theme: cyan CPU, mauve memory, muted requests, and
yellow limits (allocatable capacity for nodes). Requests are reservations,
not usage thresholds. Both charts appear when there is enough height;
`t` switches between them in a shorter terminal.

Use `[` / `]` for 5-minute, 15-minute, 1-hour, or 6-hour windows;
`Space` freezes the visible interval while collection continues, and `c`
copies a text summary with source timestamps, coverage, and peaks.
Metrics open over the list with `m`; `Esc` (or ←) closes them with the
cursor where it was.

These are session-local observations from metrics-server while browsing,
not historical monitoring data. Samples stay in memory for up to six hours,
subject to resource and sample caps, and reset when switching contexts.
Repeated source readings are deduplicated. Missing, stale, forbidden, and
partial readings leave gaps; a measured zero remains zero. Resource UIDs
keep a replacement pod or workload from inheriting its predecessor's chart.

Metrics starts with usage only. Press `a` to show or hide **Related changes**
from the Logbook. Use ↑/↓ to select a change and Enter to open its Logbook
entry; `y` opens the resource YAML throughout the app. Markers show temporal
proximity, not causation; shell commands and external changes are not included.
Charts still work when recording is disabled or history cannot be read.

A cluster opens to **All user namespaces**, individual user namespaces, and
**Infrastructure** (nodes and system namespaces). Namespace resource lists show
your pinned kinds and **All resource types…**.
Open any discovered type without changing pins. In the browser, Enter opens,
Space pins or unpins, and Esc returns to your previous view. Pin changes are
saved immediately. Cluster-scoped types are labeled explicitly.

## Terminal appearance

Safina uses your terminal’s font and size. Use a monospace font with regular
and bold faces; no Nerd Font is required. True color gives the clearest
separation between resource types, selection, and health. If names or hints
feel cramped, increase the terminal font size before changing fonts.
