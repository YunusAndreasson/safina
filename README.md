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
- Stream pod logs in follow mode with scrollback
- Auto-refresh pod/deployment lists every 5 seconds
- Remembers last used cluster and namespace
- Help screen with all keybindings

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
| : | Command palette (fuzzy jump to any resource) |

### Inspect (read-only)

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
| U | Undo (rollback) deployment |
| R | Restart app / pod |
| E | Edit YAML in `$EDITOR` (diff is shown and y/n-gated) |
| C | Cordon / uncordon node |
| D | Drain node |
| K | Kill (delete) selected resource |

### Meta

| Key | Action |
|-----|--------|
| r | Refresh |
| Tab | Move focus between tree (left) and workspace (right); ←/→ then cycles tabs |
| i | Open chat tab |
| Ctrl+B | Resource browser (pick which kinds appear as folds) |
| Ctrl+K | Switch cluster |
| Ctrl+F | Active port-forwards list |
| h | Hot pods (top-N by CPU, cluster-wide) |
| ? | Help |
| q, Ctrl+C | Quit |

### Shell View

| Key | Action |
|-----|--------|
| ↵ | Open shell (from prep screen) |
| Ctrl+D | Detach modal (the pod's shell may keep running until it self-exits) |
| Ctrl+C | Forwarded to the pod (interrupt the running command) |
| Esc | Forwarded to the pod (e.g. for vim) |

### Log View

| Key | Action |
|-----|--------|
| ↑/↓ | Scroll |
| Space | Toggle follow mode |
| g | Jump to top |
| G | Jump to bottom |
| / | Filter |
| Esc | Close logs |

### Chat View

| Key | Action |
|-----|--------|
| ↵ | Send |
| Shift+↵ | Newline |
| Tab | Back to tree |
| Ctrl+L | Clear |
| Ctrl+N | New conversation |

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
│       ├── views.go             # UI rendering
│       ├── styles.go            # Lip Gloss styles and color scheme
│       └── keys.go              # Keybinding definitions
├── go.mod
├── Makefile
└── README.md
```

## Requirements

- Go 1.21+
- Access to a Kubernetes cluster (via kubeconfig)

## License

MIT
