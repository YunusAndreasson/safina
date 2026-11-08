<p align="center">
  <a href="https://safina.run">
    <img src="site/public/og.jpg" alt="Safina's sail and name, with its Arabic name سفينة, over a container ship in a night storm: the Kubernetes TUI for teams that ship." width="760">
  </a>
</p>

<p align="center">
  Fast, keyboard-driven and written for terminal hackers.<br>
  A single Go binary on lazy client-go informers, with session history in SQLite<br>
  and an MCP server for your agent.
</p>

<p align="center">
  <a href="https://safina.run"><b>safina.run</b></a> ·
  <a href="#install">Install</a> ·
  <a href="docs/keybindings.md">Keys</a> ·
  <a href="docs/">Docs</a>
</p>

<p align="center">
  <a href="https://github.com/YunusAndreasson/safina/releases/latest"><img src="https://img.shields.io/github/v/release/YunusAndreasson/safina" alt="Latest release"></a>
  <a href="https://github.com/YunusAndreasson/safina/actions/workflows/lint.yml"><img src="https://github.com/YunusAndreasson/safina/actions/workflows/lint.yml/badge.svg" alt="verify"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT license"></a>
</p>

<br>

<img src="site/src/assets/shots/workloads.png" alt="safina listing every workload in the cluster with its kind, readiness and CPU and memory trend lines; ledger-api and search-indexer show none ready, in red. The cursor rests on the ledger-db StatefulSet, two rows below ledger-api and healthy at 1 of 1.">

## Install

```sh
curl -fsSL https://safina.run/install.sh | sh
```

Linux on x86-64 or ARM64, with glibc 2.34 or newer. The
[script](site/public/install.sh) checks the archive against the release's
SHA-256 sums and puts `safina` in `~/.local/bin` (`SAFINA_INSTALL_DIR` and
`SAFINA_VERSION` change where and which). The archives are on the
[releases page](https://github.com/YunusAndreasson/safina/releases); on macOS,
[build from source](docs/development.md#build-from-source).

Run `safina`. It reads your kubeconfig, asks which cluster, and `?` shows the
keys.

## Features

- **Jump to what's broken.** `n` goes to the next failing workload; `l`, `e`,
  `h` and `m` open its logs, events, hot pods and CPU and memory charts.
- **Chat.** Press `i` and ask what's wrong. The model gets the resource you're
  on and what you've already tried, and answers with keys to press. It can't
  press them itself.
- **Barbarossa mode.** Lowercase reads, uppercase writes: every key that
  changes the cluster needs Shift, and an edit shows its diff before it's
  applied.
- **Rollout watch.** A scale, restart or edit is followed until its pods are
  ready.
- **Logbook.** The logs and events you read and every write you made, kept on
  your machine with secrets redacted, and exportable as a postmortem timeline.
- **Nothing in the cluster.** No agent, operator or CRDs. It uses the
  kubeconfig you already have and can only do what your RBAC allows.

<table>
  <tr>
    <td width="50%"><img src="site/src/assets/shots/logs.png" alt="Inside ledger-api: one of its two pods crashing live, failing a DNS lookup of ledger-db-primary and panicking."></td>
    <td width="50%"><img src="site/src/assets/shots/chat.png" alt="The chat panel beside the workloads list, answering from the crash logs: the cluster's DNS has no record for ledger-db-primary."></td>
  </tr>
  <tr>
    <td align="center"><sub><b>Logs</b>, a crash caught live</sub></td>
    <td align="center"><sub><b>Chat</b>, reading those logs</sub></td>
  </tr>
  <tr>
    <td width="50%"><img src="site/src/assets/shots/diff.png" alt="An edit to the ledger-api Deployment waiting for confirmation in a red frame: a one-line diff changes DATABASE_URL from ledger-db-primary to ledger-db."></td>
    <td width="50%"><img src="site/src/assets/shots/history.png" alt="The Logbook: the edit to ledger-api requested, applied and followed until it completed at 2/2 ready."></td>
  </tr>
  <tr>
    <td align="center"><sub><b>Barbarossa mode</b>, the fix as a diff</sub></td>
    <td align="center"><sub><b>Logbook</b>, the edit through to 2/2 ready</sub></td>
  </tr>
</table>

## Docs

The manual lives in [`docs/`](docs/):

- [Using safina](docs/using.md): flags, features in detail, resource charts,
  terminal setup
- [Keybindings](docs/keybindings.md)
- [The Logbook, chat and investigations](docs/logbook.md)
- [Development](docs/development.md): building, checks, project layout
- [The website](site/README.md)

## Under the hood

- [Bubble Tea](https://github.com/charmbracelet/bubbletea),
  [Bubbles](https://github.com/charmbracelet/bubbles) and
  [Lip Gloss](https://github.com/charmbracelet/lipgloss) for the TUI
- [client-go](https://github.com/kubernetes/client-go) for Kubernetes
- [libghostty](https://github.com/ghostty-org/ghostty) for the embedded shell's
  terminal
- [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) for the Logbook, in
  pure Go
- [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk) for the agent
  server
- [ntcharts](https://github.com/NimbleMarkets/ntcharts) for the resource charts
- [Cobra](https://github.com/spf13/cobra) for the CLI

## License

[MIT](LICENSE). Safina (سفينة) is Arabic for ship.
