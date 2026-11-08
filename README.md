# Safina

**The Kubernetes TUI for teams that ship.**

[![verify](https://github.com/YunusAndreasson/safina/actions/workflows/lint.yml/badge.svg)](https://github.com/YunusAndreasson/safina/actions/workflows/lint.yml)
[![release](https://img.shields.io/github/v/release/YunusAndreasson/safina)](https://github.com/YunusAndreasson/safina/releases/latest)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

Safina is fast, keyboard-driven and written for terminal hackers. It's a
single Go binary on lazy client-go informers, with session history in SQLite
and an MCP server for your agent. Safina (سفينة) is Arabic for ship; the tour
is at [safina.run](https://safina.run).

![safina listing every workload in the cluster with its kind, readiness and CPU and memory trend lines; ledger-api and search-indexer show none ready, in red. The cursor rests on the ledger-db StatefulSet, two rows below ledger-api and healthy at 1 of 1.](site/src/assets/shots/workloads.png)

## Install

On Linux (x86-64 or ARM64, glibc 2.34 or newer):

```bash
curl -fsSL https://safina.run/install.sh | sh
```

The script ([`site/public/install.sh`](site/public/install.sh)) downloads the
latest release's archive, checks it against the release's `checksums.txt`,
and puts `safina` in `~/.local/bin`. `SAFINA_INSTALL_DIR` and
`SAFINA_VERSION` change where and which. The archives are also on the
[releases page](https://github.com/YunusAndreasson/safina/releases).

From source (Go 1.27 and Python 3; the Makefile fetches a pinned Zig to
build libghostty):

```bash
make build
make install  # copies to $GOPATH/bin
```

Or:

```bash
make native
PKG_CONFIG_PATH="$PWD/.build/libghostty/native/lib/pkgconfig" go build -o bin/safina ./cmd/safina
cp bin/safina /usr/local/bin/
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
- Persistent errors and notification history (`Ctrl+N`); dismiss the banner with `Ctrl+D`
- Compact resource search (`:`); `Ctrl+S` explicitly loads the selected namespace’s supported resource kinds
- Auto-refresh pod/deployment lists every 5 seconds
- Asks which cluster on every launch, so a muscle-memory keypress never lands on the wrong one
- Help screen with all keybindings

Search starts with already-loaded resources. Inside a namespace, `Ctrl+S` expands it to workloads, their pods, ConfigMaps, Secrets, Services, Ingresses, and PVCs. Partial failures are labeled. `Tab` in the palette shows resource actions. In logs, `c` switches targets and `p` opens previous-container logs when available.

Log scrollback keeps up to 10,000 recent lines and 8 MiB of raw text; individual
records retain up to 256 KiB. Older data is discarded in batches, and the status
bar reports truncation. Copy uses the retained output. Switching log targets
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
`t` switches between them in a shorter pane, and `z` expands the workspace.

Use `[` / `]` for 5-minute, 15-minute, 1-hour, or 6-hour windows;
`Space` freezes the visible interval while collection continues, and `y`
copies a text summary with source timestamps, coverage, and peaks.
`Tab` moves focus between the tree and workspace; `Esc` returns to Inspector.

These are session-local observations from metrics-server while browsing,
not historical monitoring data. Samples stay in memory for up to six hours,
subject to resource and sample caps, and reset when switching contexts.
Repeated source readings are deduplicated. Missing, stale, forbidden, and
partial readings leave gaps; a measured zero remains zero. Resource UIDs
keep a replacement pod or workload from inheriting its predecessor's chart.

Metrics starts with usage only. Press `a` to show or hide **Related changes**
from the Logbook. Use ↑/↓ to select a change and Enter to open its Logbook
entry; `d` opens the resource YAML throughout the app. Markers show temporal
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

## Keybindings

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

### Navigation

| Key | Action |
|-----|--------|
| ↑/k, ↓/j | Move cursor |
| ↵ | Drill in / confirm |
| Esc | Back / cancel |
| / | Filter rows |
| g, G | Jump to top / bottom |
| n | Next failing row |
| Ctrl+P / : | Search resources and app tools, including Logbook and Incoming proposals; Tab enables resource actions |

### Inspect and connect

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

### Change (mutates cluster state)

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

### Navigate and tools

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

### Shell View

| Key | Action |
|-----|--------|
| Esc | Detach shell |
| Ctrl+D | Send EOF to the pod's shell |
| Ctrl+C | Forwarded to the pod (interrupt the running command) |
| Ctrl+[ | Send Escape to the pod (e.g. for vim) |

### Log View

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

### Chat View

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

## Project Structure

```
safina/
├── cmd/
│   ├── safina/               # Entry point (cobra CLI), including `safina mcp`
│   └── safina-bench/         # Benchmark driver for the dedicated bench cluster
├── internal/
│   ├── tui/                  # Bubble Tea root model, update and view
│   │   ├── sections/         # Panes: tree, inspector, logs, chat, Logbook, metrics…
│   │   ├── components/       # Shared widgets: top bar, sparklines, charts, modals
│   │   ├── keys/             # Keybinding definitions
│   │   └── theme/            # Lip Gloss styles and colours
│   ├── k8s/                  # Clients, lazy informers, reads and guarded writes
│   ├── config/               # App config (~/.safina/config.yaml) and kubeconfig
│   ├── history/              # The Logbook: a local, versioned SQLite journal
│   ├── metrics/              # Bounded, session-local resource observations
│   ├── claude/               # Chat, through the user's own `claude` CLI
│   ├── investigation/        # The versioned, transport-independent proposal core
│   ├── broker/               # Local investigation state, independent of the TUI
│   ├── mcpserver/            # Investigation capabilities over MCP
│   ├── terminal/             # libghostty's terminal state, adapted to Bubble Tea
│   └── …                     # debug and error logs, local state, version
├── scripts/                  # libghostty bootstrap (pinned Zig and source), bench setup
├── bench/                    # Benchmark fixtures and baseline
├── plugins/safina/           # Claude Code plugin for investigations
├── site/                     # safina.run (Astro), see site/README.md
├── Makefile
└── README.md
```

## Requirements

- Go 1.27.3+ (CI reads the version from `go.mod`)
- For source builds: Python 3.12+, a C compiler, and pkg-config; CGO must be enabled.
  `make` downloads checksum-verified Zig and Ghostty sources into `.build/libghostty`
  and builds the static terminal library. The first build can take several minutes.
  The bootstrap supports Linux/macOS on x86-64/ARM64; CI builds Linux on both.
  Installed binaries do not require Zig or a separate libghostty shared library.
- `make lint` / `make fmt` download and run golangci-lint 2.15.0
- Access to a Kubernetes cluster (via kubeconfig)

`k8s.io/kube-openapi` stays on the revision compatible with client-go 0.37.2.
The newer November revision requires structured-merge-diff v7 and does not
compile with Kubernetes' v6 types. Upgrade these together when client-go supports it.

New benchmark profiles use Kubernetes 1.37.2 and pause 3.10.2. Existing profiles
are not upgraded by the seed script. Re-record benchmark baselines after a
deliberate cluster upgrade before comparing performance.

## Development checks

Run `make verify-all` before submitting changes to check both the Go application
and website. CI runs both gates independently. The website dependencies must be
installed first with `cd site && pnpm install --frozen-lockfile`.

- `make verify`: validates the lint configuration, checks module tidiness and
  checksums, checks formatting, runs static analysis and race tests, then builds
  both Go commands. `make test` is the faster unit-test loop.
- `make site-verify` (or `cd site && pnpm verify`): runs ESLint with zero warnings
  allowed, Astro's strictest TypeScript checks, and the production build.
- `make fmt`: applies Go formatting. Lint tools and the website package manager
  are pinned so local and CI checks use the same versions.

Go checks include all vet analyzers except struct-padding suggestions, unchecked
assertions, context propagation, enum completeness, Unicode bidi controls,
serialization tags, and security checks including integer narrowing and file
permissions. Security exceptions are documented at specific calls; only the
error-return check duplicated by `errcheck` is disabled globally. Partial UI
state predicates are explicitly marked; the central key dispatch stays exhaustive.

The website uses `astro/tsconfigs/strictest`, including checked indexed access,
exact optional properties, unused-code detection, and missing-return checks.
ESLint adds strict type-aware TypeScript rules, Astro checks, and accessibility
rules. Browser logic lives in `site/src/scripts/*.ts` so promises and DOM access
receive full type-aware linting. New inline browser scripts are rejected. Small
accessibility exceptions preserve Safari list semantics and keyboard scrolling.

Unit tests use fake Kubernetes clients and temporary configuration directories;
they do not need a cluster. Some tests open loopback sockets, so restricted
sandboxes need to allow those. Benchmark targets are separate and require the
dedicated benchmark cluster described above.

For mutation tests, check both the intended change and what must stay unchanged.
`internal/k8s/edit_contract_test.go` runs one edit safety contract across all eight
editable resource kinds: preserve fields and Secret bytes, retain concurrency
metadata, and make no API calls for invalid input. Add new editable kinds to that
table. Run it with `go test ./internal/k8s -run 'TestEdit' -count=1`.
The companion `httptest` case uses the real Kubernetes REST client to check the
request and conflict response; fake clients alone do not enforce resource-version
conflicts or emulate server-side Secret conversion. Use small HTTP tests where
those boundaries matter, and keep ordinary object behavior in the fake-client
tests. Both run in the existing test and race gates without a cluster or new tools.

## License

[MIT](LICENSE)

## Terminal library evaluation

Safina uses `libghostty-vt` through the pinned Go bindings for its embedded shell.
It replaces vt10x and the custom cell renderer and key-sequence encoder. Bubble
Tea places the cursor, and Lip Gloss pads the formatted grid into the panel.
The native source and toolchain pins are in `scripts/libghostty.json`.

`bench/terminal` retains an isolated comparison of the previous vt10x adapter,
Charm's `x/vt`, and `libghostty-vt`, including versions, checksums, contract results,
and raw benchmark samples.

The November 7, 2026 run covered 17 selected contracts: screen updates, Unicode
across stream boundaries, key modes, paste, resize reflow, device replies, and
synthetic vim/less recordings. Libghostty passed all 17; Charm passed 14; the
previous vt10x adapter passed 9. These are targeted compatibility checks, not a
complete terminal conformance suite. The legacy key/paste/reply tests mirror
Safina's adapter, while screen tests inspect the emulator directly.

| Backend | Contracts passed | Median parse time | Median plain snapshot time |
| --- | ---: | ---: | ---: |
| vt10x | 9/17 | 380 µs | 240 µs |
| Charm x/vt | 14/17 | 742 µs | 274 µs |
| libghostty-vt | 17/17 | 20 µs | 79 µs |

Parsing overwrites a fixed 120×40 screen without scrolling. Snapshot timings
include adapter and text-normalization work, not Safina's styled rendering.
These three-sample measurements came from a shared development host and are not
performance regression thresholds or whole-app speedups. Go allocation counters
exclude native allocations. Adding statically linked libghostty to the stripped
comparison test executable increased it from 3,903,751 to 6,609,536 bytes; this
is not a measurement of Safina's eventual binary size.

To reproduce on Linux x86-64 with Go, Python 3.12+, a C compiler and pkg-config:

```sh
cd bench/terminal
make native             # Downloads verified Zig and pinned Ghostty into .build
make compare            # Intentionally fails the incompatible baseline cases
make ghostty-contracts  # Runs only the libghostty contracts
make bench              # Three samples, one Go execution thread
```

The cold native build can take several minutes. `build_native.py --cache PATH`
can reuse a separate build directory; pass `NATIVE_PREFIX=PATH/native` to the
other Make targets. The synthetic recordings can be regenerated with
`python3 capture.py` when vim and less are installed. Production adapter tests
in `internal/terminal` and `internal/tui/sections` cover styled output, cursor
placement, Unicode stream boundaries, alternate screens, keys, paste, replies,
resize and cleanup. Session tests cover ordered input, backpressure and shutdown.
These do not establish compatibility with every interactive program or validate
macOS/ARM64 release packaging.

## Cluster overview cache

Reopening a cluster can show its last namespace/workload overview immediately
while a fresh read runs in the background. The refresh indicator says **cached**
until validation succeeds. Opening a namespace or resource still uses the normal
read path; cached previews do not seed metric history or action checks.

The cache keeps up to four overviews for 24 hours, in memory and in
`$XDG_CACHE_HOME/safina/overviews` (normally `~/.cache/safina/overviews` on Linux;
the OS user-cache directory is used on other platforms). Files are owner-only.
The versioned JSON file is capped at 8 MiB, with a 2 MiB limit per overview;
oversized overviews use the normal loading path. It stores namespace names,
workload names/kinds and last-known health, never credentials, Secret values,
logs, full manifests or metric samples. Cache keys separate context, server,
CA configuration, username/impersonation identity and namespace classification preferences.

Repeated refreshes update memory immediately; disk writes are limited to once
a minute per retained cluster during a session. First visits save immediately.

Deleting that directory clears the disk cache. Unreadable, expired or corrupt
entries are ignored. If the fresh read fails after a preview appears, the
preview stays labelled **cached** and the normal read-error banner remains visible.

## Logbook: saved cluster activity

Safina automatically saves timestamped actions across sessions under
`~/.safina/history`. Open `:` and choose **Logbook** (typing `history` finds it too) to search the selected
cluster/resource's history. Press `/` to filter, `Enter` for record/evidence
details, `a` for all resources in the current scope, `t` to cycle retained/24-hour/7-day
windows, `e` to export that scope,
or `c` to attach a bounded history/evidence preview to a chat draft. Review the
draft and press Enter to send it. The browser loads the newest 500 records;
use CLI time filters for a particular incident or a larger export.

The logbook records inspection, browsing, explicit refresh/search, Secret reveals,
shell/debug-container connections, port-forward lifecycle, scale, restart,
rollback, edits, deletion, cordon/uncordon, drain, and individual eviction
attempts. Resource identities and operation IDs remain attached when you switch
clusters. API acceptance is distinct from controller readiness. Missing or
superseded generations, cancelled requests, interrupted sessions, and monitoring
failures do not establish successful completion.

This is a **local Safina activity journal**, not cluster-wide or tamper-proof
Kubernetes auditing. It cannot capture independent kubectl/CI changes, activity
while Safina is closed, or commands typed inside a pod shell. Cluster IDs are
fingerprints of endpoint/CA configuration; endpoint or CA changes create a new
identity. Context names are retained as searchable aliases. An unfinished action
after a crash has an unknown outcome; it is never replayed automatically.

### Offline investigation and Claude

These commands work without kubeconfig, a running cluster, or Claude installed:

```bash
safina history list --cluster production --resource Deployment/api
safina history show OPERATION_OR_EVENT_ID --format jsonl
safina history export --cluster production \
  --since 2026-11-07T08:00:00Z --until 2026-11-07T12:00:00Z \
  --output ./incident-2026-11-07
```

Additional filters: `--namespace`, `--session`, and `--limit` (default 10,000).
Exports refuse to overwrite a directory. They contain a timestamped timeline,
versioned JSONL records, available evidence, a coverage manifest, and a
postmortem template. Start Claude Code in the export directory and ask it to
reconstruct the incident, cite event IDs, and separate facts from hypotheses.
The journal is evidence, not instructions. Nothing is uploaded automatically and
Safina does not modify global Claude memory.

Safina chat automatically attaches at most 50 relevant history records from the
last 24 hours, capped at 16 KiB. Raw evidence is included only when you explicitly
attach it. This context is sent through the existing Claude integration when you
submit the message.

### Storage, privacy, and configuration

Action records are stored in `history.sqlite` using SQLite transactions and indexed
queries. Evidence remains in JSONL files, and exports keep their portable JSONL format.
Action records retain 90 days by default, evidence 14 days, subject to separate
256 MiB/1 GiB limits. The action limit counts encoded record payloads; SQLite indexes,
free pages, and its WAL add disk overhead. Freed pages are reused. The oldest action
records or closed evidence files are removed when their pool reaches its limit.
Active captures reserve up to 10 MiB each. The UI warns persistently if
recording fails; cluster operations remain available. Expired/missing evidence,
capacity pruning, truncation, and incomplete JSONL tails are surfaced in queries
or exports. Exported bundles are outside automatic retention; remove them yourself
when no longer needed. Existing `errors.log` and optional debug logging retain
their separate behavior and are not imported into history.

On the first writable open, existing action JSONL files are imported in one transaction.
Event IDs, operation links, and redacted content are preserved. Close older Safina
sessions before upgrading; older versions cannot read or append to the new database.
The original `actions/` files remain as a recovery backup and are not reimported or
subject to automatic retention after migration. Remove that backup once you have
verified the import and no longer need it. A failed import is retried on the next open;
queries report an incomplete migration instead of silently showing an empty history.
Read-only history commands can still read an unmigrated JSONL directory.
The broker's separate investigation journal is unchanged by this history migration.

Viewed container logs are captured before display filtering/scrollback eviction,
with original and observed timestamps. Captures stop at 10 MiB; lines over
256 KiB are omitted with a marker. Existing Kubernetes log-request limits still
apply, so this is not continuous collection of every container. Workload evidence
contains selected identity, image, replica, revision, and condition fields;
manifest edits record changed paths rather than arbitrary values.

Secret values, arbitrary manifest bodies, kubeconfig credentials, shell
transcripts, and free-form API error bodies are excluded. Recognizable tokens,
password assignments, URL credentials, and private keys are redacted before
storage. Redaction cannot identify every application secret or personal datum;
review bundles before sharing and add patterns or disable evidence where needed.
Directories/files are created with owner-only permissions (0700/0600).

Optional settings in `~/.safina/config.yaml`:

```yaml
history:
  enabled: true
  directory: /home/you/.safina/history
  evidence: true
  actionDays: 90
  evidenceDays: 14
  actionBytes: 268435456
  evidenceBytes: 1073741824
  redactPatterns:
    - 'customer-secret-[A-Za-z0-9]+'
```

Omitted/zero numeric settings use defaults. Invalid redaction expressions disable
recording and surface an error rather than silently collecting unredacted data.
Use `evidence: false` for action metadata without saved log/event bodies or
before/after evidence; `enabled: false` disables new history recording.

### Design references

The design combines Kubernetes' distinction between request metadata and request
bodies with timestamped operational outcomes. Kubernetes documents that only the
latest rotated container log file is available through the logs API, motivating
capture during investigation: [logging architecture](https://kubernetes.io/docs/concepts/cluster-administration/logging/)
and [audit levels](https://kubernetes.io/docs/tasks/debug/debug-cluster/audit/).

Separating source time from observation time follows the
[OpenTelemetry log data model](https://opentelemetry.io/docs/specs/otel/logs/data-model/).
Evidence-linked timelines support the mitigation/outcome analysis described in
[Google SRE's postmortem guidance](https://sre.google/sre-book/postmortem-culture/).
Bounded context follows [Claude's memory guidance](https://code.claude.com/docs/en/memory);
credential exclusions, capacity controls, and failure tests follow
[OWASP logging guidance](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html).

### Investigation MCP and operator-reviewed Deployment changes

The local proposal core is available to Go clients through
`internal/broker/api.InvestigationClient`. It starts the broker on demand and
can create investigations, submit typed alternatives, and retrieve pending work
while the TUI is closed. In Safina, open `:` and choose **Incoming proposals**.
The pending count appears above the workspace. Select a proposal with Enter,
read the summary, previous attempts and warnings, and use `d` for the complete executable diff
(`Tab` cycles summary, diff and captured evidence). `e` edits replicas,
`container=image`, or the pinned rollback revision as a new immutable revision;
restart has no editable parameters. `x` rejects a proposal. If controller warnings require an
override, `o` explicitly acknowledges them. `A`, then `y`, approves the exact
reviewed change once. No-op changes cannot be approved.

The broker checks Deployment identity/version and controller observations again
before sending one guarded patch. A stale review must be refreshed and approved
again. Approval and execution intent must reach both the durable journal and
logbook before sending. The broker continues readiness monitoring after the TUI
closes; reopen Incoming proposals and select the operation to see its outcome.
Controller completion does not establish application recovery.

Deployment scaling, a named regular container's image, restart with a frozen
reviewed timestamp, and rollback to an exact owned ReplicaSet revision are
supported. Image/restart patches preserve other fields; rollback replaces the
reviewed pod template. Diffs mask literal environment values and apply configured
redaction; executable template bytes stay in broker memory. An interrupted or
ambiguous write is never automatically replayed.

Run `safina mcp` from an MCP client to use the official Go SDK stdio adapter.
Stdout contains protocol messages only. The nine tools are `list_contexts`,
`start_investigation`, `inspect_workload`, `read_events`, `read_logs`,
`query_history`, `get_evidence`, `submit_proposals`, and `get_investigation`.
No tool approves, rejects, overrides or executes a Kubernetes mutation.
Investigation creation, proposals and evidence capture persist local data and
are not advertised as universally read-only.

Start requires an explicit context and returns the last 24 hours of relevant
retained activity (at most 50 records / 16 KiB). A supplied workload target
prioritizes that resource and related workload records; without a target the
brief covers the pinned cluster. `query_history` supports deeper time windows
and resource filters with at most 100 records / 16 KiB per response. Operator
reviews show recent attempts of the same action kind, including failed and
uncertain outcomes. Missing history does not prove inactivity.

Inspection selects fields and excludes environment/Secret values. Logs are
bounded to 200 lines / 16 KiB; events and related objects are limited to 100 per
list. Responses identify scope, observation time, truncation and permission or
coverage failures. Evidence is untrusted data, never instructions. Captured
responses are redacted before return/storage and retained with the investigation
(up to 64 captures of 16 KiB each, independently of logbook retention). Disable
new evidence persistence with `history.evidence: false`; live reads then return
without a stored evidence ID. Proposal evidence IDs must belong to that
investigation; fetching scoped historical evidence returns a new captured ID.
Historical Secret contents are excluded, and expired evidence stays explicit.

Existing manual workflows remain available. After upgrading an already-running
broker, explicitly stop it with `safina broker stop`; the next client starts the
new binary. Load the [Claude plugin](plugins/safina/README.md) to use
`/safina:investigate`. Release validation progress and limitations are recorded
in the [workflow plan](docs/plans/claude-safina-workflow.md).

```sh
safina serve                 # foreground broker (normally started by a client)
safina broker status         # readiness, pending logbook delivery, recording gaps
safina broker stop           # explicit shutdown; keeps investigations on disk
```

The default owner-only Unix socket is `~/.safina/run/broker.sock`; durable
investigations live in `~/.safina/investigations/journal.jsonl`, independently of
logbook retention. `serve` accepts `--runtime-dir`, `--state-dir`, `--kubeconfig`
and `--history-dir`; status/stop accept `--runtime-dir`. These commands do not
choose the current kubeconfig context implicitly. Creating an investigation
requires an explicit context; proposals remain unverified until broker review.

Clients keep independent session IDs and reuse the same idempotency key when
retrying a submission with an uncertain response. Restart restores pending work;
recovered execution intents become unknown and are never replayed. Logbook
recording failures retain a durable delivery queue and appear in broker status.
The socket and peer checks are local access controls, not a sandbox against
arbitrary code running as the same user.
