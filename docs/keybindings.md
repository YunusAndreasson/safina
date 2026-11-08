# Keybindings

Press `?` in safina for help.

Lowercase shortcuts inspect resources or open connections; uppercase change
shortcuts open a review or confirmation before mutating cluster state. Shell
access can run commands that change the cluster and is not inherently read-only.

The footer normally shows inspection and navigation shortcuts. On terminals
with enhanced keyboard support, hold Shift to reveal changes for the selected
resource; release it to restore the normal hints. This only reveals shortcuts:
it does not arm an action or bypass confirmation. The right-hand footer shows
whether the hold gesture is available. Press `?` for the complete **Inspect**
and **Change** groups on every terminal. Shortcut keys use the same hotkey color
in tabs, help, pickers, and empty states; keys that confirm changes use danger red. Filters, chat, and dialogs keep their
normal typing behaviour.

## Navigation

| Key | Action |
|-----|--------|
| ↑/k, ↓/j | Move cursor |
| ↵ | Drill in / confirm |
| Esc | Back / cancel |
| / | Filter rows |
| g, G | Jump to top / bottom |
| n | Next failing row |
| Ctrl+P / : | Search resources and app tools, including Logbook and Incoming proposals; Tab enables resource actions |

## Inspect and connect

| Key | Action |
|-----|--------|
| d | Resource YAML |
| m | CPU / memory metrics for the selected workload, pod, or node |
| l | Logs |
| e | Events |
| s | Shell into pod (falls back to a netshoot debug container) |
| v | Reveal Secret (decoded) |
| f | Port-forward a Pod or Service |
| y | Yank selection / payload to clipboard (OSC52) |

## Change (mutates cluster state)

| Key | Action |
|-----|--------|
| S | Scale deployment / statefulset |
| U | Roll back deployment |
| R | Restart app / pod |
| E | Edit YAML in `$EDITOR` (diff is shown and y/n-gated) |
| C | Cordon / uncordon node |
| D | Drain node |
| K | Delete selected resource |

Node drain uses kubectl's filtering and eviction handling. Unmanaged pods block
drain unless force is explicitly enabled; terminating pods remain pending until
they disappear. Safina requires the eviction API and never falls back to direct
pod deletion. Cancelling or timing out after cordoning leaves the node cordoned;
inspect the remaining pods, then explicitly uncordon when appropriate.

## Navigate and tools

| Key | Action |
|-----|--------|
| r | Refresh |
| → / ← | In the tree: ← goes up a level, → opens Inspector. In the workspace: arrows switch tabs; ← from Inspector returns to the tree |
| Tab | Toggle focus between tree and workspace |
| z | Expand / split the reading pane (logs, events, YAML, hot pods, forwards) |
| i | Open chat tab |
| Ctrl+B | All resource types (Enter opens; Space pins/unpins) |
| Ctrl+K | Switch cluster |
| Ctrl+F | Active port-forwards list |
| h | Hot pods (top-N by CPU, cluster-wide) |
| ? | Help |
| q, Ctrl+C | Quit |

## Shell View

| Key | Action |
|-----|--------|
| Esc | Detach shell |
| Ctrl+D | Send EOF to the pod's shell |
| Ctrl+C | Forwarded to the pod (interrupt the running command) |
| Ctrl+[ | Send Escape to the pod (e.g. for vim) |

## Log View

Logs use compact UTC times with date markers. Copying logs preserves the original timestamps and text.
The `/` filter accepts smart-case text searches or a `jq:` predicate for JSON logs,
for example `jq: .level == "error"` or `jq: select(.duration_ms > 1000)`.
A truthy result keeps the original line; `false`, `null`, and no output exclude it.
Kubernetes timestamp prefixes are removed only for JSON parsing. Non-JSON and
unfinished JSON lines do not match. Expressions have a 50 ms batch budget and a
256-result limit per line; errors appear in the log pane until the filter is edited.
JSON lines over 256 KiB are skipped. jq filtering uses the embedded gojq library;
no separate jq executable, environment access, or module loader is needed.

| Key | Action |
|-----|--------|
| ↑/↓ | Scroll |
| Space | Toggle follow mode |
| g | Jump to top |
| G | Jump to bottom |
| / | Filter |
| t | Toggle compact UTC times / original full timestamps |
| w | Toggle line wrapping |
| y | Copy the filtered logs with original timestamps |
| Esc | Close logs |

## Chat View

| Key | Action |
|-----|--------|
| ↵ | Send |
| Shift+↵ | Newline |
| Tab | Back to tree |
| Ctrl+L | Clear |
| Ctrl+N | New conversation |
| Shift+←/→ | Select text |
| Ctrl+←/→ | Move by word |
| Ctrl+G | Select all input |
| Ctrl+Shift+C | Copy selected input |

On terminals supporting OSC 7501, Safina reports when chat is working or a
cluster change is waiting for confirmation, even while another tab is active.
