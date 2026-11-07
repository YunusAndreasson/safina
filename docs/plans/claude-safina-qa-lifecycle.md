# Broker lifecycle probes — 2026-11-07

This round probes local broker startup, cancellation and request delivery after
the completed implementation and earlier QA rounds. Tests use Unix sockets,
temporary files and local API fixtures; no live Kubernetes cluster was used.

## Confirmed defects and fixes

1. **Named pipes could hang startup or delivery before validation.** Local state
   opens checked regular-file type only after a potentially blocking `open`.
   A FIFO at `broker.log` waits for a reader; a read-only FIFO waits for a writer.
   Both probes failed before the fix. Open nonblocking until file type is
   validated, then restore the caller's blocking flag for accepted regular files.
   Symlink, hardlink, ownership and truncate protections remain enforced. This
   does not promise bounded latency for arbitrary devices or filesystem faults.
2. **Oversized typed requests returned a misleading protocol error.** Sending
   a 300 KiB summary exceeded the server's request limit before it could decode
   the correlation ID. The client returned `invalid broker response`, after
   unnecessarily starting the broker. Marshal and check the complete request
   envelope before startup; return typed `invalid_argument` on size failure.
   Client and both server routes share the same 256 KiB limit.
3. **Server write deadline expired before the client deadline.** An 11-second
   read-only resolver completed and committed an investigation, but the server's
   10-second write deadline discarded the reply while the client allowed 15
   seconds. A Unix-socket test reproduced EOF. Keep the 15-second client timeout
   and allow 20 seconds for server writes. The same test must now receive the
   saved investigation and return its identical ID on an idempotent retry.
   Transport failures remain possible; no automatic uncertain mutation retry is
   introduced by this change.

The filesystem failures are recorded in `/tmp/safina-qa4/fifo-repro.log`; client
failures are in `client-repro.log`. Test workers explicitly unblock and close the
FIFO peer after reproducing the old failure, so the probes leave no blocked open.

## Additional probes and review

Cancellation while waiting for either `startup.lock` or `broker.lock` already
returned the caller's deadline without invoking the launcher, creating a socket
or contacting Kubernetes. Both new regression cases passed without a code fix.
Existing concurrent startup, crash recovery and permission tests remain part of
the full verification gate.

Claude CLI performed a focused read-only review of `client.go` and `server.go`,
using only Read/Grep and no MCP servers in an isolated directory. It identified
the oversized-request and deadline mismatch cases; both were independently
reproduced before edits. Startup/shutdown ordering produced no additional
confirmed defect in that review. Review output is retained in `/tmp/safina-qa4`.

Changes are confined to local-state opening, broker request limits/deadlines,
regression tests and QA documentation. Baseline hashes are
`/tmp/safina-qa4/baseline.json`. Unrelated website and other working-tree changes
were preserved; no staging, commits or deployment occurred.

## Validation

Focused regression tests passed with the race detector (`fixes.log`), including
both FIFO modes, both lock waits, oversized-request rejection and the 11-second
request with idempotent retry. macOS ARM64 cross-compilation passed; no macOS
runtime test is claimed. `git diff --check` passed.

Full `make verify-all` passed (exit 0): module checks, formatting, zero lint
issues, all Go race tests, Go build, website lint, Astro diagnostics and production
build. Log: `/tmp/safina-qa4/verify.log`. No unresolved check failures remain.
