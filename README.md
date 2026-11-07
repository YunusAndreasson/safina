# Safina

A lightweight terminal-based Kubernetes navigation tool — like `k9s` but simpler.

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
- Remembers last used cluster and namespace
- Help screen with all keybindings

Search starts with already-loaded resources. Inside a namespace, `Ctrl+S` expands it to workloads, their pods, ConfigMaps, Secrets, Services, Ingresses, and PVCs. Partial failures are labeled. `Tab` in the palette shows resource actions. In logs, `c` switches targets and `p` opens previous-container logs when available.

Notification history keeps the latest 50 messages for the session. `Enter` opens the full message; `r` retries supported failed reads without replaying a mutation. Scale/restart monitoring checks controller status for up to five minutes and distinguishes acceptance, readiness, and monitoring failures.

## Installation

```bash
make build
make install  # copies to $GOPATH/bin
```

Or:

```bash
go build -o bin/safina cmd/safina/main.go
cp bin/safina /usr/local/bin/
```

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

Lowercase keys are read-only verbs (safe to fat-finger); uppercase or chord
keys mutate cluster state.

### Navigation

| Key | Action |
|-----|--------|
| ↑/k, ↓/j | Move cursor |
| ↵ | Drill in / confirm |
| Esc | Back / cancel |
| / | Filter rows |
| g, G | Jump to top / bottom |
| n | Next failing row |
| : | Search loaded resources (type to search; Tab enables action shortcuts) |

### Inspect and connect

| Key | Action |
|-----|--------|
| d | Describe (YAML) |
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

### Meta

| Key | Action |
|-----|--------|
| r | Refresh |
| Tab | Move focus between tree (left) and workspace (right); ←/→ then cycles tabs |
| z | Expand / split the reading pane (logs, events, YAML, hot pods, forwards) |
| i | Open chat tab |
| Ctrl+B | Resource browser (choose which resource kinds appear in the tree) |
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
├── cmd/safina/main.go           # Entry point (cobra CLI)
├── internal/
│   ├── config/
│   │   ├── config.go            # App config persistence (~/.safina/config.yaml)
│   │   └── kubeconfig.go        # Kubeconfig resolution and context listing
│   ├── k8s/
│   │   ├── client.go            # Client manager per context
│   │   ├── pods.go              # Pod listing, deletion, log streaming
│   │   └── deployments.go       # Deployment listing, scaling, rollback
│   └── tui/
│       ├── model.go             # Bubble Tea root model
│       ├── update.go            # Message handling and state transitions
│       ├── view.go              # UI rendering
│       ├── theme/               # Lip Gloss styles and color scheme
│       └── keys/                # Keybinding definitions
├── go.mod
├── Makefile
└── README.md
```

## Requirements

- Go 1.27.3+ (CI reads the version from `go.mod`)
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

## License

MIT
