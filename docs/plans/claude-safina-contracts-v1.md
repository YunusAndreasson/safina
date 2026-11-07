# Claude–Safina implementation contracts, version 1

M0 deliverable, 2026-11-07. These are implementation contracts for M1–M4,
not claims that the broker or MCP tools already exist. The
[workflow plan](claude-safina-workflow.md) remains the product authority.

## Baseline and ownership

Inspected HEAD: `4fad722188a5cba20198fe9e936c1836a5ee53bc` plus the dirty
working tree. No applicable `AGENTS.md` was found in the repository or its
ancestor directories. Repository checks are defined by `Makefile`; website
installation and checks are documented in `site/README.md`.

The initial tree has 52 modified tracked files (800 insertions, 107 deletions,
plus screenshot changes). All are pre-existing: README/CLI/config logbook
integration, Go dependency classification, Kubernetes mutation recording and
edit validation, TUI history/chat/operation tracking, and website copy, layout,
scripts, and screenshots. Untracked foundations include `internal/history/`,
`internal/k8s/history.go`, `internal/k8s/edit_contract_test.go`,
`cmd/safina/history_test.go`, TUI history implementations/tests,
`site/src/components/Aboard.astro`, history screenshots, and the workflow plan.
These files must remain intact. M0 owns only this new contract document and
progress edits to the workflow plan. Nothing is staged or committed by M0.

## Existing behavior to preserve

| Area | Working-tree behavior and integration constraint |
| --- | --- |
| Logbook | `internal/history/store.go` writes version 1 JSONL with generated record/session IDs, sequence, target, operation, parameters and evidence references. Files are created 0600 in 0700 directories. Append syncs but is deliberately fail-open: it returns an empty ID and exposes health on failure. This API is not a durable execution authorization gate. |
| Retention/query/export | `query.go` preserves timestamp order, surfaces malformed/unsupported records and retention gaps, and exports records plus evidence and a manifest. Defaults: actions 90 days/256 MiB; evidence 14 days/1 GiB. Segment and quota locks support existing independent writers. Query's cluster filter also matches context aliases; broker reads must enforce exact pinned fingerprints separately. |
| Evidence | Captures are bounded to 10 MiB, lines to 256 KiB; built-in/custom credential redaction is best effort. `HistorySnapshot` selects fields, excluding full manifests, arbitrary annotations, environment values and Secret data. Existing evidence IDs alone do not establish investigation authorization. |
| Chat | Existing history context defaults are 24 hours, 50 records, 16 KiB; explicit attachments are bounded. In-app Claude prompt labels history untrusted and requires distinguishing accepted from completed. Preserve this path independently of MCP. |
| Manual edit | `internal/k8s/edit.go` supports multiple resource kinds, including Secrets. It rejects multiple YAML documents, duplicate/unknown fields, changed name/namespace and missing resourceVersion; uses full-object Update. Edit history records paths, not values. These capabilities must not leak into proposal tools. |
| Diff | `internal/tui/view.go` already has a scrollable manual edit diff; `TestEditDiffFitsAndScrolls` covers 80×24 wrapping and reaching the end. The plan's reference to a truncated presentation is not a reason to replace this working manual surface. New proposal review needs its own complete diff and narrow-terminal coverage. |
| Scale | `ScaleWorkload` rejects negative replicas, dispatches Deployment/StatefulSet, and uses GetScale then UpdateScale. ResourceVersion comes from that immediate read, not a previously reviewed proposal. Reusing this helper unchanged would lose the review binding. |
| Restart | `RestartWorkload` generates the timestamp at execution and sends a strategic merge patch without a reviewed version/UID precondition. Broker restart needs a frozen timestamp and explicit concurrency checks. |
| Rollback | `RollbackDeployment` chooses the highest owned ReplicaSet revision below the current revision at execution time, fetches it, copies its template, removes `pod-template-hash`, then updates the Deployment. Broker rollback must instead pin and revalidate ReplicaSet identity, revision and template. |
| Readiness | `WorkloadProgressForGeneration` checks UID/generation, controller observed generation, desired/updated/available counts and progress deadline failure. `RecordMutationResponse` binds monitoring to the accepted response. Scale response generation may need a resourceVersion-matched read. Current TUI polls every two seconds, stops after five minutes, and distinguishes superseded/unknown monitoring results; it does not survive TUI exit. Broker owns the new monitoring lifetime. |

## Package and capability boundaries

The following package names are reserved for implementation; no empty scaffolds
are needed in M0.

| Package | Responsibility and dependency boundary |
| --- | --- |
| `internal/investigation` | Versioned domain types, validation and state transitions. No TUI, transport, Kubernetes client, or mutation dependency. |
| `internal/investigation/store` | Exclusive broker writer lock, append/sync/recovery, indexes, idempotency and durable logbook outbox. Separate from history retention. |
| `internal/broker` | Process lifecycle, Unix transport, sessions, investigation service, worker scheduling and monitoring. Serializes claims/group decisions through the durable store. |
| `internal/broker/api` | Versioned request/response DTOs and separate investigation-client and operator-client interfaces. The investigation interface contains only the nine tool capabilities below. |
| `internal/proposal` | Prepare/revalidate typed Deployment mutations and compute review diffs. Execution accepts only a durable broker claim bound to a prepared revision. No arbitrary YAML or command entry point. |
| `internal/mcp` | Official Go MCP SDK adapter, depending only on the investigation-client interface and domain DTOs. No operator-client, executor or Kubernetes mutation client. Diagnostics to stderr. Pin SDK at M3 after checking compatibility. |
| Existing packages | CLI wires serve/mcp/status/stop; TUI uses operator-client for review and approval. `internal/k8s` supplies bounded reads/readiness; `internal/history` remains compatible with manual operations and exports. `internal/claude` continues serving in-app chat. |

Use separate investigation and operator socket routes/dispatchers, with an
explicit allowlist on each. Never select operator capability via an MCP request
flag. Owner-only sockets/directories and peer checks constrain local access but
are not a sandbox against arbitrary same-user code.

## Versioned records and invariants

Persistent journal envelope v1: `version`, `eventId`, monotonic `sequence`, UTC
`time`, `type`, and typed `payload`. IDs are opaque generated identifiers;
references are validated, never interpreted as paths. Times use RFC3339Nano.
Unknown versions or corruption inside the journal block state mutation; a
truncated final record is isolated, reported and excluded before resuming append.
Keep the interrupted bytes for diagnosis. Replay rebuilds state, never sends a
Kubernetes mutation. Sync new files and parent directories as well as records
before acknowledging durability.

| Record | Required contract |
| --- | --- |
| Investigation | ID, originating client session ID, pinned cluster fingerprint plus display context alias, optional target, summary, evidence references, creation time. Never silently choose a cluster or fall back to an alias fingerprint when cluster identity cannot be established. |
| Target | Cluster fingerprint, API group/version, kind, namespace, name, UID. v1 executable targets are `apps/v1` Deployments. |
| Proposal revision | Proposal ID, positive immutable revision, investigation/group IDs, author provenance (agent/operator), target, observed resourceVersion/generation/time, typed action, rationale, evidence references and expected controller verification. New revision supersedes the previous pending revision. |
| Prepared review | Broker-generated review ID bound to proposal revision, current identity/version, exact mutation/diff digest, frozen action values, warnings and expected effect. A state change invalidates it and requires a fresh review; UID replacement blocks the proposal. |
| Decision | ID, exact review/revision, select or reject, operator provenance, UTC time, explicit warning overrides and operation ID for selection. Overrides cannot be supplied by agent tools. |
| Execution | Operation ID, unique claim, exact review/digest, durable request intent, API acceptance with returned UID/resourceVersion/generation where available, progress and outcome. |
| Evidence | ID, investigation and cluster scope, resource identities, observation/source timestamps, redacted content, truncation and coverage metadata. Expired/unavailable references remain explicit. |

Action is a strict discriminated union; reject unknown or mixed fields:

- `scale`: integer `replicas` in 0..2147483647; no-op detection and explicit zero effect.
- `image`: exact regular container name and nonempty image reference; reject missing/ambiguous container targets; preserve every unrelated field.
- `restart`: no agent/operator free-form parameters. Broker freezes the RFC3339 UTC restart annotation value when preparing the revision.
- `rollback`: exact positive revision resolved to an owned ReplicaSet UID, resourceVersion and template digest/content. Revalidate that source before execution; never resolve “previous” at send time.

Revision states: pending → superseded, rejected, or selected. A selected group
has exactly one selected revision; its other pending alternatives become
unavailable atomically. Editing/refresh only applies to pending work. Reconsidering
a selected/rejected group requires a new group and fresh review. Repeated actions
are permitted as new proposals with earlier attempts displayed.

Selection, sibling invalidation, decision, execution intent, claim and outbox
events must be one durable transition (one journal event can contain the entire
transaction). Only its successful durable append allows the sole executor to
send once. Competing/retried approvals return the existing operation or a conflict.
A failed append means no request. An uncertain append/response after send never
causes resend. Startup treats an intent without conclusive result as interrupted/
unknown and reconciles through reads. Matching present state alone is not causal
proof. Accepted operations may resume read-only monitoring. Completed means
controller completion, never application recovery.

Outbox events have stable IDs persisted with transitions. Add optional
investigation/proposal/revision/decision linkage to history v1 records; preserve
old records, queries and exports. Implement an error-returning, idempotent append
path for broker delivery; existing `Append` regenerates IDs and cannot provide
this guarantee. Persist delivery acknowledgements; retry only journal delivery,
never the Kubernetes write. Required decision/intent records must be durable
before execution; post-write delivery failures remain visible as incomplete
recording. Disabling or pruning the ordinary logbook cannot remove pending state
or bypass the broker's durable gate.

## Tool and local transport contract v1

Local request envelope: `version: 1`, `requestId`, `method`, typed `params`.
Response: matching version/request ID and either `result` or a structured error.
Use codes `invalid_argument`, `not_found`, `scope_mismatch`, `conflict`,
`unsupported_version`, `permission_denied`, `recording_unavailable`, and
`unavailable`. Errors never imply a mutation should be replayed.

Reject unknown request fields, including approval/execution/override fields.
Idempotency key scope is `(sessionId, method, key)`; persist normalized request
digest and original result atomically. An identical retry returns the original
IDs; a different payload with the same key conflicts. `submit_proposals` stores
one group atomically, never a partially accepted batch. Reconnect keeps the same
client session ID; independent Claude sessions use independent IDs.

| MCP tool | v1 inputs → result |
| --- | --- |
| `list_contexts` | No inputs → context aliases and usable identity metadata, no credentials. |
| `start_investigation` | Session ID, idempotency key, explicit context, optional target, summary → pinned investigation and relevant history brief. |
| `inspect_workload` | Investigation ID and Deployment reference → selected fields and related owned ReplicaSets/Pods/controller observations. |
| `read_events` | Investigation ID, scoped resource filter, time window, limit → bounded events and evidence IDs. |
| `read_logs` | Investigation ID, pod/container identity, previous/current selection, time window, line/byte limits → bounded redacted logs and evidence IDs. |
| `query_history` | Investigation ID, optional resource/time filters and limit/cursor → exact-cluster records, linked outcomes and gaps. |
| `get_evidence` | Investigation ID, evidence ID, bounded offset/limit → scoped redacted content or explicit unavailable status. |
| `submit_proposals` | Session ID, investigation ID, idempotency key, alternatives with typed actions/rationale/evidence → pending group and immutable revisions. No Kubernetes mutation. |
| `get_investigation` | Investigation ID → proposals, decisions, outcomes and recording health. |

Every evidence/history result includes scope, observed/source timestamps when
known, omitted/truncated indicators and coverage warnings. Start brief defaults:
24 hours, 50 relevant records, 16 KiB. Deep reads may request wider windows but
remain server-bounded; finalize per-tool hard caps and pagination in M3. Evidence
must belong to the pinned investigation or an explicitly validated same-cluster
historical reference. Cross-namespace dependency reads obey RBAC. Exclude Secret
values, redact before storage/return, and sanitize terminal controls before UI
rendering. Unsupported assertions are marked unverified.

Operator-only methods will prepare/revise/reject/select reviews; selection takes
an exact review ID/digest and explicit warning acknowledgements, never arbitrary
mutation bytes. Status/stop are explicit CLI lifecycle actions. Connection startup
holds an exclusive process lock through readiness, checks protocol compatibility,
and never blindly unlinks a live broker socket. Stop does not erase pending work;
it records interruption for unfinished operations. M1 must test concurrent startup,
retry/recovery, and that the investigation dispatcher cannot reach execution.

## M1 implementation notes

- `internal/investigation`, `internal/investigation/store`, `internal/broker`,
  `internal/broker/api`, and `internal/localstate` now implement the durable core
  and local transport. The current investigation interface exposes only start,
  submit and get. Remaining read tools and the SDK adapter belong to M3.
- The local wire protocol uses strict JSON envelopes over HTTP on a Unix socket.
  `/v1/investigation` dispatches the three allowed methods; `/v1/status` and
  `/v1/stop` are lifecycle endpoints, absent from the investigation client
  interface. No operator/approval route is registered yet. M2 must validate
  broker-prepared reviews before invoking internal store decision primitives.
- A journal transaction carries a typed investigation snapshot plus its optional
  idempotency receipt and logbook event. This keeps selection, siblings, decision,
  intent and claim in one synced record. Requests are capped at 256 KiB, submission
  batches at 16 alternatives, and journal records at 8 MiB. A limit failure blocks
  the transition. Large-investigation compaction/pagination is future work.
- Idempotency hashes typed requests before redaction; persisted human text is
  redacted with configured history patterns. Distinct secret-bearing requests do
  not silently collapse into the same retry. Evidence IDs are rejected until
  scope-validated evidence registration exists in M3; assertions stay unverified.
- `history.AppendDurable` writes stable-ID v1 records atomically under the existing
  quota lock. Optional investigation/proposal/revision/decision fields preserve
  legacy readers and exports. Delivery acknowledgements are journaled separately;
  uncertain acknowledgements can safely redeliver the same event.
- Internal selection returns a fresh claim only once, after sync. Repeated calls
  return the operation without a send grant. Restart converts unconfirmed intents
  to unknown; accepted operations remain available for M2 read-only monitoring.
  Neither selection nor outcome recording has a transport entry point in M1.
- Linux and macOS peer UID checks complement 0700 directories and 0600 sockets/files.
  Starter and writer locks have separate lifetimes. The child runs in its own
  session; readiness validates protocol/configuration, and clients reconnect by
  socket rather than relying on a cached PID.


## M2 implementation notes

Preparation currently lives in `internal/broker/review.go` and execution in
`internal/broker/execution.go`, with the operator wire types in
`internal/broker/operator`. This keeps the scaling increment close to broker
ownership rather than introducing the planned `internal/proposal` package before
multiple actions need it. M3 can extract shared preparation logic without
changing the approval boundary.

- `/v1/operator` and `broker.OperatorClient` are separate from the investigation
  API. Methods are inbox, prepare, revise (replicas only), reject and approve.
  The MCP-facing client still exposes only start/submit/get. Same-user arbitrary
  code remains outside the workflow boundary; the operator endpoint is not a
  sandbox or a remote authorization service.
- A prepared review binds the proposal revision, pinned cluster and target UID,
  resourceVersion, observed generation, before/after replicas, full scalar diff,
  controller signature, required overrides and digest. Reviews expire after 15
  minutes, are invalidated by a refresh of the same proposal, and do not survive
  broker restart. The durable decision embeds the accepted review for recovery.
- Approval revalidates a single kubeconfig snapshot, live UID/version and HPA
  observations. The Kubernetes request is JSON Patch: test UID, test
  resourceVersion, add `/spec/replicas`. It preserves all other requested fields.
  This follows [Kubernetes conditional patch semantics](https://kubernetes.io/docs/reference/using-api/api-concepts/#updates-to-existing-resources).
  Client-go's default Retry-After retries are explicitly disabled with
  `MaxRetries(0)`; HTTP redirects are also disabled. Conflicts require another
  review; ambiguous transport/server responses never trigger a replay.
- HPA and detected GitOps ownership require exact logged override codes. Failed
  HPA discovery requires an explicit `hpa-coverage` override. No detection proves
  absence of another controller. HPA and Deployment are separate resources: an
  HPA can still appear/change after the last read; it cannot be transactionally
  preconditioned with the Deployment patch. The UI warns about controller reversal.
- The synced decision/intent is delivered to the logbook before sending. Delivery
  failure retires that approval without a send; journal failures fail closed.
  Post-send recording errors cannot grant another claim. Decisions/outcomes carry
  target, proposal, revision, operation, operator, override and reviewed-replica
  linkage in compatible logbook records.
- Accepted responses persist UID/version/generation. Broker-owned reads resume
  after restart and require the accepted generation, controller observation and
  desired updated/available/total replicas for completion. Replacement/later
  generations supersede an operation. Deadline failure and read coverage gaps
  remain visible. Recovered intent or ambiguous writes remain unknown even if
  replicas match; reads may establish replacement, never causal success.
- The TUI has a scrollable Incoming proposals surface with immutable editing,
  rejection, summary/diff/evidence navigation and a separate final approval gate.
  It sanitizes ANSI/control/bidi text, wraps narrow terminals, refreshes outcomes
  independently of cluster navigation and does not cancel broker work on exit.
  Captured evidence references and matching historical attempts remain M3; the
  evidence tab explicitly states that coverage is unavailable in M2.


## M3 implementation notes

- The official Go MCP SDK is pinned at v1.8.0, checked against its
  [release notes](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.8.0)
  and repository examples. `internal/mcpserver` depends only on the investigation
  interface; `safina mcp` uses stdio with a 256 KiB inbound frame cap. Nine typed
  tools expose contexts/start/inspect/events/logs/history/evidence/submit/get.
  The SDK rejects unknown arguments and the broker independently rejects unknown
  envelope/parameter fields. Operator methods never become tools. Creation,
  submission and evidence capture advertise local state changes.
- Captured evidence is a journaled, investigation-scoped record with a broker ID,
  pinned cluster, namespace/resource, kind, observation time, bounded redacted
  text and explicit coverage/truncation metadata. Captures survive restart and
  proposal references must resolve within the investigation. Raw historical
  evidence is accessible only through a retained same-cluster record, never by
  arbitrary path. Historical Secret contents are excluded. Captures are capped
  at 64 per investigation and 16 KiB each; a full capture allocation fails with
  recording unavailable. `history.evidence: false` returns live data without a
  persistent ID instead of silently ignoring the user's capture preference.
- Inspection selects Deployment identity/version, desired replicas, status and
  container names/images, plus owned ReplicaSets and Pods. It excludes full
  manifests, literal environment values and Secret values. Logs include source
  timestamps and are bounded to 200 lines / 16 KiB; event and dependency lists
  use limits and report missing/partial coverage. Read permission failures are
  explicit evidence warnings, not empty-success assertions.
- Start automatically returns a 24-hour / 50-record / 16-KiB history brief, scoped
  to the selected target/related workload records or the pinned cluster if no
  target was supplied. Explicit history lookup supports time windows and
  namespace/resource filtering (100 records / 16 KiB maximum). Exact cluster
  equality is rechecked after the legacy query layer's context-alias matching.
  Operator review shows matching action-kind attempts, preserving failed,
  superseded and unknown outcomes; broader history remains available by tool.
- `internal/broker/actions.go` prepares all four typed actions. Image changes
  patch the reviewed container index's image under UID/version guards; restart
  patches annotations with the frozen timestamp. Rollback pins an exact owned
  ReplicaSet revision/UID/version, removes the generated pod-template hash label,
  and revalidates that snapshot before applying its reviewed template. No lookup
  of "previous" occurs after approval. Unrelated fields remain outside image and
  restart patches. ReplicaSet and Deployment checks cannot form a cross-object
  transaction; the sent rollback bytes always represent the reviewed snapshot.
- Prepared reviews retain raw patch bytes only in broker memory (`json:"-"`).
  Durable decisions contain the action, mutation/template digest, redacted full
  diff and source revision metadata, sufficient for read-only monitoring without
  a reusable executable payload. Literal environment values are masked in diffs;
  changed values remain visibly marked. Accepted template outcomes must match the
  reviewed template digest; admission changes produce an unknown outcome rather
  than a false causal success. All actions use the existing single-send claim,
  pre-send recording gate and independent readiness worker.
- The TUI supports image and rollback edits through immutable revisions, shows
  stored evidence and previous matching attempts, and keeps the same diff and
  final-approval gate for every action. Restart is parameterless.

### M4 implementation notes

- `plugins/safina` packages a local Claude Code plugin, default `.mcp.json` and
  namespaced investigation skill. It runs the installed `safina mcp`, adds no
  operator capability, hooks or permission grants, and documents provider
  disclosure and same-user limits. Tool schemas remain the runtime contract.
- TUI `--runtime-dir`, `--state-dir`, `--history-dir` and MCP `--history-dir`
  permit isolated trials with the existing kubeconfig flags. The broker owns its
  configuration until stopped; CLI paths do not relocate general app settings.
- Edit and final confirmation prompts precede the scrollable summary and reset
  the viewport, so long agent text cannot hide the active prompt on entry.
- Operator parameter edits preserve original agent rationale/verification prose.
  Follow-up must use the selected typed action/review for actual expected values
  and distinguish stale agent prose. No model-authored text defines execution.
- The real Claude/disposable-cluster trial and exact validation limits are in
  [the release trial](claude-safina-release-trial.md). This is local validation,
  not a published marketplace package or macOS runtime certification.

### Follow-up persistence hardening — 2026-11-07

Outcome and interruption records may now carry `payload.executionUpdate`
(investigation ID plus one execution) instead of repeating a full investigation.
The linked outbox event shares the same synced transaction. Replay requires an
existing operation, unchanged decision/claim, an allowed state transition and
UID/resourceVersion for API acceptance. Other transitions retain snapshots.
This permits uncertainty recovery even when the preceding snapshot nearly fills
the 8 MiB journal limit. Old full-snapshot records remain readable; older binaries
reject the new payload field, so downgrade after new writes is unsupported.

Before journal commit, derived logbook records must fit the history line limit
after redaction. Delivery is serialized in journal sequence order and does not
hold the investigation state lock during logbook I/O. Acknowledgements remain
durable and retry-safe; newly queued records wait for the next delivery pass.
