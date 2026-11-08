# Contributing

Bug reports and pull requests are welcome. For anything bigger than a fix,
open an issue first, so we can agree on the shape before you write the code.

## Build and check

[docs/development.md](docs/development.md) has the details. In short, you need
Go 1.27, Python 3, a C compiler and pkg-config; `make build` fetches a pinned
Zig and builds libghostty, which takes a few minutes the first time.

Before a pull request:

- `make verify` passes: formatting, lint, race tests and the build. The tests
  use fake Kubernetes clients, so no cluster is needed.
- For a change to the website, `cd site && pnpm install --frozen-lockfile &&
  pnpm verify` passes too. The site has its own guide, with strict rules for
  copy and screenshots: [site/README.md](site/README.md).
- One change per pull request, with a commit message in the repository's
  style: `fix(tui): …`, `feat(site): …`, `docs: …`.

## What safina won't do

Some behaviour is deliberate and changes to it need a strong case:

- Lowercase keys only read. Every write needs Shift and a confirmation.
- Safina never picks a cluster for you, not even the last one you used.
- Chat answers with keys to press; it never presses them.
- Nothing is installed in the cluster.

## Security issues

Not in issues or pull requests: see [SECURITY.md](SECURITY.md).

## Conduct and license

Everyone here follows the [Code of Conduct](CODE_OF_CONDUCT.md). By
contributing, you agree your work is released under the
[MIT license](LICENSE).
