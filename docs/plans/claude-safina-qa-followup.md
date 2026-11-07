# Follow-up failure probes — 2026-11-07

The implementation milestones M0–M4 remain complete. This second QA round
investigated persistence boundaries, slow asynchronous responses and broker
configuration lifetime. It uses local store, Unix broker and TUI regression
fixtures; it does not claim a second live Kubernetes trial.

## Findings and fixes

1. **Slow inbox polling starved valid replies.** Every five-second poll advanced
   the response epoch even while an earlier request was pending. Requests taking
   longer than the poll interval could never update the inbox. Permit only one
   inbox request at a time and clear that state on its current success/error.
2. **Failed detail retrieval left the UI busy.** The zero-value detail returned
   with an error failed the selected-investigation filter before clearing busy.
   Only attach successfully retrieved details; apply detail identity filtering to
   successful replies while preserving request epoch checks for every reply.
3. **An oversized derived history record poisoned delivery.** Requests and
   investigation journal records have different limits from logbook records.
   Validate the redacted, serialized history record before committing its parent
   transition. Rejection leaves no proposal/outbox entry and later valid work
   proceeds. A Unix broker regression combines two individually bounded requests
   into a history record exceeding its limit; no Kubernetes call occurs.
4. **Slow delivery blocked state access and backlog order was random.** Serialize
   deliveries separately, snapshot detached records in journal sequence order,
   release the state lock during the logbook callback, then durably acknowledge
   each successful record. Concurrent state changes remain available and newly
   queued records are delivered on the next pass. This does not impose a deadline
   on arbitrary filesystem I/O.
5. **Crash recovery could exceed the journal limit.** A valid intent record near
   8 MiB became too large when startup appended uncertainty to another full
   snapshot. Outcomes and interrupted intents now persist a compact execution
   update and its linked outbox record in one synced transaction. Replay validates
   the predecessor, immutable decision/claim, state transition and API acceptance
   provenance. The boundary test reopens twice with an additional observation,
   preserving the large summary; corrupt compact references fail closed.

The first two store/UI regressions failed before their fixes (`repro.log`).
The detail, slow-delivery and recovery tests also failed against the original
implementation (`repro2.log`). Focused race tests subsequently passed, including
compact-record corruption, ordered delivery, concurrent writes and repeated
recovery (`fixes.log`, `fixes2.log`, `regressions.log`).

## Compatibility and configuration

- Replayed a **copy** of the prior 23,216,169-byte QA journal through the new
  binary twice: all 106 investigations remained retrievable, with full summaries,
  proposals and decisions unchanged and execution claims absent from public
  responses. Used an isolated kubeconfig pointing only at localhost port 9; no
  old or live cluster endpoint was configured. Both brokers stopped cleanly.
- Old full-snapshot journal records remain readable. Newly written compact
  `executionUpdate` payloads use the v1 envelope. Earlier binaries reject that
  unknown field; downgrade after new writes is not supported. Do not describe
  this as bidirectional compatibility.
- A running broker intentionally retains its history/redaction configuration
  until explicitly stopped. Existing documentation already specifies this
  lifetime; changing client options does not hot-reconfigure a broker.
- This is finite regression coverage, not a tamper-proof journal, general
  pagination/retention solution or guarantee about all storage failures.

## Claude CLI usage and ownership

A focused read-only Claude CLI review inspected only `store.go` and `incoming.go`,
with `Read,Grep` tools, no MCP servers and an isolated temporary workspace. It
returned three concrete findings (detail errors, delivery locking/order and
near-limit recovery), independently reproduced before implementation. The main
agent performed edits and verification; Claude did not modify the repository.

Changes are restricted to investigation store/outcome handling and tests, Incoming
TUI state and tests, one broker regression, and these plan/contract documents.
Existing website and other working-tree changes were preserved. No commits,
staging, publication or production writes were performed. Local diagnostic logs,
the review, binaries and compatibility script are in `/tmp/safina-qa2`; the
baseline file hashes are `baseline.json` there.

## Final validation

`make verify-all` passed (exit 0): module verification, formatting, lint (zero
issues), all Go race tests, Go build, website lint, Astro diagnostics and production
build. Log: `/tmp/safina-qa2/verify-final2.log`. Native and macOS ARM64 binaries
were produced; native compatibility smoke passed, with no macOS runtime claim.
`git diff --check` passed.

Intermediate full checks found two formatting differences, two static-analysis
false positives on test-owned temporary paths, and an error-construction lint
finding. These were corrected before the final pass. The compatibility script's
first assertion incorrectly counted investigations without proposals in the inbox;
correcting that expectation allowed verification of all 106 full investigations.
No unresolved check failures remain.
