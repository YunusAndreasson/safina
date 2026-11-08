# Development

## Build from source

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
