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

| Key | Action |
|-----|--------|
| ↑/k | Move up |
| ↓/j | Move down |
| Enter | Select / drill down |
| Esc | Go back |
| Tab | Switch between Pods/Deployments |
| d | Delete selected pod |
| s | Scale deployment |
| r | Rollback deployment |
| l | View pod logs |
| S | Shell into pod (auto-falls-back to a netshoot debug container if the pod has no shell) |
| R | Manual refresh |
| ? | Help |
| q | Quit |

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
| Esc | Close logs |

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
