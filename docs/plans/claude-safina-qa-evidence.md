# Evidence and interrupted-write probes — 2026-11-07

This additional QA round follows the completed M0–M4 workflow and earlier
adversarial reports. It targets binary log content, interrupted HTTP log streams,
warning-slice ownership and torn compact acceptance records. Tests use local
HTTP Kubernetes fixtures and temporary journals, not a new live cluster trial.

## Confirmed defects

- **Binary logs lost useful text at truncation.** A non-UTF-8 byte near the start
  of a long log caused the UTF-8 boundary loop to remove every subsequent byte.
  The reproducer returned only `prefix` instead of the useful following lines.
  Normalize invalid sequences to the Unicode replacement character before
  bounding text. Store capture does the same before redaction and truncation.
  Tests cover the HTTP log path, short binary strings, byte limits, split Unicode
  characters and persisted/reopened evidence.
- **Interrupted log streams discarded partial observations.** A server declaring
  a larger Content-Length than it sends reproduces `unexpected EOF` after useful
  log bytes. Previously no observation was captured. Now partial text goes
  through ordinary bounded/redacted capture, marked truncated with an explicit
  incomplete-coverage warning. The regression verifies retrieval by evidence ID
  and absence of the plaintext credential from the captured text. Raw transport
  errors were already sanitized by the public protocol; no credential leak was
  reproduced or claimed.

Both reproducers failed before the fixes (`repro.log` and `stream-repro.log` in
`/tmp/safina-qa3`). The focused broker/store race suites passed after fixes
(`fixes.log`).

## Additional probes

- Mutating input or returned capture warning slices does not change authoritative
  state: journal application already deserializes a detached snapshot. Added a
  regression; no implementation change was needed for this hunch.
- Tear a compact API-acceptance record after its first byte, in the middle and
  immediately before its final newline. Check exact preserved tail bytes,
  recovery to unknown, refusal to issue another execution claim, and another
  successful reopen. These are disk-level fixtures, not physical power-loss or
  filesystem durability certification.

## Claude CLI and ownership

Claude CLI read only the two evidence implementation files with Read/Grep tools,
no MCP servers and an isolated workspace. It independently found the binary-byte
bug and suggested the interrupted-stream probe. Both were independently tested;
its suggestion that raw errors leaked publicly was checked against the protocol
and was not adopted. Review output and all test logs are in `/tmp/safina-qa3`.

Changed evidence read/capture implementations and broker/store tests only, plus
this report and workflow progress. The baseline hashes are
`/tmp/safina-qa3/baseline.json`. Unrelated changes, including current website copy
and AGENTS.md, remain intact. No staging, commits or deployment occurred.

## Final validation

`make verify-all` passed (exit 0): module checks, formatting, zero lint issues,
all Go race tests, Go build, website lint, zero Astro errors/warnings and the
production build. Final log: `/tmp/safina-qa3/verify-final.log`. The broker and
store suites passed with the new HTTP and torn-record probes included.
`git diff --check` passed. One intermediate check requested a tagged switch in a
test; it was corrected before the final run. No unresolved check failures remain.
