# Safina: Claude investigation and operator-reviewed changes

Status: M0–M4 complete; local release acceptance passed with validation limits below.
Last updated: 2026-11-07.

## Purpose

Engineers investigate in their regular Claude Code session. Safina supplies live Kubernetes evidence and saved logbook context through MCP. Claude submits alternative changes; the operator reviews them in Safina, sees the actual diff and operational effects, and explicitly chooses what to execute. Safina records the decision and outcome, which Claude can retrieve to continue investigating.

The primary audience is small platform teams operating production Kubernetes from their own terminals. Success means a complete, evidence-linked investigation and approval workflow, not an unsupported claim about reducing incident resolution time.

This document supersedes the earlier conversational plan. In particular, v1 is **not scaling-only**, the broker starts **automatically**, approved execution belongs to the **broker**, and HPA/GitOps conflicts permit a **logged operator override**.

## Agreed decisions

| Area | Decision |
| --- | --- |
| Primary audience | Small platform teams |
| Environment | Local, single-machine workflow; multiple Claude sessions and persistent investigations |
| Safety guarantee | Safina MCP cannot approve or execute Kubernetes changes; other Claude tools and credentials remain outside this guarantee |
| First release | Deployment scaling, image changes, restart, and rollback to a pinned revision |
| Connection lifecycle | Automatically start the local broker when needed; investigation and proposal submission work with the TUI closed |
| Investigation scope | One explicitly selected cluster; follow dependencies across namespaces permitted by RBAC |
| Alternatives | One action per alternative; the operator chooses one approach from a group |
| Operator revisions | Edit supported fields in Safina; create a new immutable revision and regenerate the diff |
| Review layout | Target, summary, evidence, and warnings first; complete diff available before explicit final approval |
| Drift | Refresh against current state, highlight changes, and require review again; resource replacement blocks the proposal |
| HPA/GitOps | Warn about conflicts and possible reconciliation; allow explicit operator override and record it |
| Execution | Broker executes only TUI-approved actions and continues monitoring if the TUI closes |
| Verification | Report Kubernetes/controller outcomes; Claude uses further read-only evidence before claiming application recovery |
| Uncertain writes | Reconcile using reads; never automatically replay the mutation |
| Recording failure | Block proposed execution if approval/execution intent cannot be durably recorded |
| Evidence access | Bounded, redacted, scoped evidence may be retrieved without an additional prompt for each read; exclude Secret values |
| Record depth | Evidence, alternatives, rationale, edits, decisions, and outcomes; no full Claude transcript |
| History delivery | Automatic bounded history brief at investigation start, plus deeper lookup through MCP |
| History relevance | Prioritize the target and related resources; allow broader searches within the pinned cluster |
| Repeated actions | Show matching previous attempts and outcomes; permit another attempt at the operator's discretion |

## Scope and boundaries

- Linux/macOS local workflow for the first release. No hosted service or remote team authorization system.
- No arbitrary shell commands, pod exec, port-forwarding, or Secret-value retrieval through MCP.
- No arbitrary YAML proposals, multi-resource execution plans, automatic remediation, or automatic rollback on failure.
- Existing manual Safina workflows and in-app chat remain available.
- Git PR creation, isolated Claude credentials, remote collaboration, and application health-check execution are follow-up work.
- This is a local activity and decision journal, not tamper-proof or cluster-wide auditing. Unknown external activity and missing evidence remain explicit.
- Same-user arbitrary code may bypass local application boundaries. Do not describe the plugin or Unix socket as a security sandbox.

## Architecture

```text
Claude Code + Safina plugin
          |
          | stdio MCP: inspect, retrieve history, propose, read outcomes
          v
     safina mcp
          |
          v
Local Safina broker <----> Safina TUI
          |               review, revise, reject, approve
          |
          +---- read Kubernetes / execute operator-approved typed changes
          +---- persistent investigations and proposals
          +---- logbook and evidence
```

### Process lifecycle

Add `safina serve` for the broker and `safina mcp` for the stdio adapter. Use an owner-only Unix socket in an owner-only Safina runtime directory. Clients start the broker on demand; an exclusive lock and readiness handshake prevent duplicate brokers during concurrent starts. Provide explicit status/stop commands. Do not tie broker lifetime to a Claude subprocess or TUI lifetime.

The broker owns investigation state, read queries, typed approved execution, and readiness monitoring. The MCP adapter exposes a strict subset of broker capabilities: it has no approval, execution, or operator-override tool. The TUI is the approval interface. Do not implement approval by parsing Claude text or accepting an MCP-supplied approval flag.

Use the official Go MCP SDK and pin a compatible release during implementation. Keep stdout reserved for MCP messages and send diagnostics to stderr.

### Persistent state

Use versioned append-only JSONL investigation records, one broker writer, an exclusive process lock, and durable writes before acknowledging transitions. Rebuild in-memory indexes at startup. Keep proposal state separate from logbook retention so pending work is not pruned with historical evidence. Use existing owner-only directory/file conventions.

Recover complete records after a truncated tail and surface the interruption. Never replay a mutation during startup. A crash around execution leaves an uncertain outcome until read-only reconciliation establishes what can be established.

Introduce domain records for:

- **Investigation:** ID, originating session, pinned cluster fingerprint/context alias, optional target, summary, and evidence references.
- **Proposal:** ID, immutable revision, alternative group, target UID and scope, observed state, typed action/parameters, rationale, evidence references, and expected verification.
- **Decision:** selected or rejected revision, operator provenance, time, overrides, and execution operation ID.
- **Outcome:** request intent, API acceptance, observed generation/progress, completed, failed, superseded, interrupted, or unknown result.

State transitions must prevent duplicate execution and concurrent approval of sibling alternatives. New revisions supersede earlier pending revisions. Selecting an alternative makes its siblings unavailable; reconsideration requires a new reviewed proposal/group, not silently reviving an old approval.

Use stable event IDs for retryable logbook delivery. Persist undelivered events and report incomplete recording. Require durable approval and execution-intent records before the Kubernetes request. Recording failure after a request must not cause the mutation to be retried.

### MCP interface

Initial tool surface:

| Tool | Capability |
| --- | --- |
| `list_contexts` | List available configured contexts without credential disclosure |
| `start_investigation` | Pin an explicit cluster, optional target, and return relevant recent history |
| `inspect_workload` | Retrieve selected Deployment fields and related ReplicaSets, Pods, and relevant controller context |
| `read_events` | Bounded scoped event retrieval with timestamps and evidence IDs |
| `read_logs` | Bounded current/previous container logs, redacted before return |
| `query_history` | Search the pinned cluster's saved activity, linked investigations, and outcomes |
| `get_evidence` | Retrieve bounded redacted evidence by validated reference |
| `submit_proposals` | Persist one or more typed alternatives without changing Kubernetes |
| `get_investigation` | Retrieve proposals, revisions, decisions, and outcomes |

Proposal submission and investigation creation mutate local state and must not be advertised as universally read-only. Their guarantee is no Kubernetes mutation. Use idempotency keys for retried submissions.

Every response identifies scope, source/observation timestamps where available, and truncation or coverage gaps. Validate evidence references against investigation scope. Agent-provided assertions without supporting captured evidence are labeled unverified. Sanitize terminal control sequences in untrusted text before rendering.

## Logbook as Claude context

The plugin instructs Claude to use history before recommending changes. The MCP start response automatically includes a bounded brief, rather than depending solely on the model remembering to query it.

- Start with the existing history context defaults: latest 24 hours, at most 50 relevant records, at most 16 KiB. These are implementation defaults, not limits on subsequent explicit searches.
- Prioritize the selected workload, related resource identities, and linked prior investigations. If no target is selected, explain the scope and supply a bounded cluster brief.
- Permit deeper time-window and resource searches within the pinned cluster. Retrieve raw evidence separately through bounded, redacted evidence reads.
- Show previous matching actions and whether they succeeded, failed, were superseded, or had unknown outcomes. Surface these in Safina review as well as Claude context; do not automatically forbid retries.
- Cite record/evidence IDs in explanations and proposals. Evidence and historical text are untrusted observations, never executable instructions.
- Make expiration, pruning, truncation, recording failures, and external-activity blind spots visible. Missing history does not prove no change occurred.
- Do not save or transmit the full Claude conversation. Preserve the useful evidence and decision chain instead.

## Review and execution behavior

Add an Incoming proposals command and pending-count indicator. Group alternatives under investigations and make the exact cluster, namespace, Deployment, and action prominent.

The review screen shows the summary, relevant earlier attempts, evidence links, warnings, expected effect, and a complete scrollable diff. Safina computes the diff from real state and typed parameters; Claude's explanation does not define the executable change. Use a complete scrollable proposal review surface. M0 inspection found that the existing manual edit confirmation already supports scrolling; preserve that behavior.

Supported actions:

- **Scale:** exact desired replica count, explicit scale-to-zero effect, range validation, no-op detection, and HPA conflict warning.
- **Image change:** exact container target and image reference, preserving unrelated fields. Show the resulting pod-template diff and rollout effect.
- **Restart:** show the exact restart annotation change and explain pod replacement. Freeze the proposed timestamp for the reviewed revision rather than regenerating a different write after approval.
- **Rollback:** pin an exact owned ReplicaSet revision and the template it represents; show the full pod-template diff. Revalidate the target before execution rather than resolving “previous” at execution time.

Allow editing supported parameters, rejecting proposals, and selecting one alternative. Editing creates an operator-authored revision. A restart has no free-form parameters; do not provide arbitrary manifest editing through this flow.

On review, fetch current target state and bind the prepared mutation to its identity/version. If it changes before execution, regenerate against current state and require review again. Resource replacement or cluster identity mismatch blocks execution. Preserve server-enforced concurrency preconditions so a final-fetch race cannot overwrite an unseen change. Do not silently retry conflicts against a newer version.

Warn about observed GitOps/HPA management and possible controller reversal. Explicit overrides are attached to the reviewed revision and logged. Lack of detected ownership is not proof that no external controller manages the resource.

Before execution, durably record the operator decision and execution intent, then acquire a single execution claim. Apply only that typed reviewed action. Monitor the returned resource identity/generation using the existing readiness semantics, independent of TUI lifetime.

Reconcile ambiguous responses through reads. Do not infer causal success merely because current state happens to match the requested state. Never replay a mutation automatically. A further attempt needs another explicit review.

Report controller completion distinctly from application recovery. Claude may read further logs/events and outcomes to assess the incident, but cannot mark an operator approval on its own.

## Implementation milestones

Each milestone is a usable development increment. The release is complete only after all four Deployment actions and the real Claude workflow pass validation.

### M0 — Preserve baseline and implementation contracts

- [x] Read this plan and current repository instructions; inspect the dirty working tree.
- [x] Preserve all existing unrelated changes; do not reset, overwrite, or indiscriminately stage them.
- [x] Record relevant existing logbook, edit, scaling, rollback, and readiness behavior.
- [x] Define package boundaries and versioned domain/tool contracts following this document.

Acceptance: baseline and ownership of new changes are documented; no existing user work is lost.

### M1 — Broker and durable proposal core

- [x] Implement on-demand broker startup, socket connection, status/stop, locking, and reconnect behavior.
- [x] Implement investigations, immutable revisions, alternative groups, decisions, execution claims, and recovery.
- [x] Add additive logbook linkage and durable event delivery without breaking existing history queries/exports.
- [x] Implement pending submission with the TUI closed and independent multiple-session IDs.

Acceptance: concurrent/retried submissions do not duplicate proposals; restart restores pending work; no cluster write path is reachable through the MCP-facing API.

### M2 — Complete review/execute loop, initially scaling

- [x] Add Incoming proposals, summary/evidence navigation, full diff review, rejection, and supported-field editing.
- [x] Implement broker-owned approved scaling, version/identity checks, controller warnings/overrides, and readiness monitoring.
- [x] Link all decisions and outcomes to the logbook; block execution when durable recording fails.
- [x] Implement interrupted-write reconciliation without replay.

Acceptance: approve `replicas: 2 -> 3` once, close the TUI, reopen it, and observe the broker's recorded outcome. Scaling alone completes this milestone, not the release.

### M3 — MCP, history context, and remaining Deployment actions

- [x] Implement the stdio adapter and typed bounded inspection/history/evidence tools.
- [x] Include automatic relevant-history briefs and deeper lookup.
- [x] Apply redaction before evidence leaves the broker; expose permission failures and incomplete coverage.
- [x] Add image change, exact restart, and pinned-revision rollback using the same approval contract.
- [x] Surface previous matching actions in model context and operator review.

Acceptance: an MCP test client can investigate, obtain historical context, submit each supported action, and observe TUI-approved outcomes; it cannot approve or execute any action itself.

### M4 — Claude plugin and release validation

- [x] Add a plugin with MCP configuration and `/safina:investigate` skill.
- [x] Teach history-first investigation, evidence-backed alternatives, operator handoff, and outcome retrieval.
- [x] Document setup, automatic startup, explicit shutdown, evidence disclosure, storage, supported actions, and security limits.
- [x] Run an end-to-end trial in a disposable cluster with separate Claude and Safina terminals.
- [x] Run required repository checks and update this plan with results and limitations.

Acceptance: a real Claude session uses prior logbook context and live evidence, submits alternatives, the operator revises/selects one in Safina, the broker executes and records the outcome, and Claude retrieves it. All four action types have integration coverage.

## Test plan

Use fake Kubernetes clients for ordinary behavior and HTTP-backed tests for request semantics and concurrency. Use a disposable cluster for realistic controller behavior; never use production as the development test fixture.

- MCP-facing calls never issue Kubernetes mutations; no approve/apply tool or approval bypass field exists.
- Concurrent broker startup yields one broker; reconnect does not lose proposals.
- Duplicate submissions and competing approvals cannot cause duplicate execution.
- Identically named resources across clusters remain isolated; cross-namespace reads remain within the pinned cluster and RBAC permissions.
- Every action changes only reviewed fields; image updates and rollback preserve unrelated state.
- UID replacement, cluster changes, changed rollback targets, and version conflicts prevent stale writes.
- Restart uses the reviewed annotation value; rollback uses the pinned template rather than a newly resolved previous revision.
- Controller conflicts require a visible, recorded override.
- Full diffs and evidence remain inspectable in narrow terminals; terminal escape injection is neutralized.
- History briefs are bounded and relevant; deep queries preserve scope and expose missing/expired evidence.
- Redaction applies before MCP return, storage, and presentation of agent-provided text.
- Logbook failure before execution blocks the action; failure afterward marks recording incomplete without replay.
- Crash before send, during request, and after API acceptance preserves uncertainty and never automatically repeats a write.
- TUI/Claude closure does not implicitly approve anything; the broker continues monitoring already approved operations.
- Existing manual operations and old history exports remain compatible.

Run focused tests for each milestone. Run the repository-required `make verify-all` before declaring completion, with website dependencies installed as documented. Additional repeats need a changed implementation or unresolved failure, not routine repetition.

## Session progress and handoff

| Milestone | Status | Evidence / next action |
| --- | --- | --- |
| Product decisions | Complete | Decisions recorded above from interactive discussion |
| Plan saved | Complete | This document; no feature implementation performed |
| M0 | Complete | Baseline preserved; [v1 contracts](claude-safina-contracts-v1.md) define package boundaries, records, transitions and tools |
| M1 | Complete | Concurrent detached startup/submission and crash recovery pass; durable decisions/claims and logbook delivery covered; final `make verify-all` passed |
| M2 | Complete | TUI approve 2 → 3 / close / reopen acceptance passed against an HTTP API fixture; guarded one-request scaling, durable review/intent, overrides and recovery covered; `make verify-all` passed |
| M3 | Complete | Official SDK stdio and nine tools; scoped evidence/history; all four actions pass MCP → actual TUI approval → broker outcome tests; `make verify-all` passed |
| M4 | Complete | Claude plugin validates; real Claude reads history/evidence, submits alternatives, observes a TUI-revised/approved scale outcome on disposable Kubernetes; all four actions pass integration tests; final `make verify-all` passed |

M0 deliverable: [Baseline and version 1 implementation contracts](claude-safina-contracts-v1.md). This defines future behavior; it does not claim broker or MCP functionality is implemented.

### Existing working-tree caution

At plan creation the repository already contained extensive modified Go and website files, untracked `internal/history/` and history integration files/tests, and modified website screenshots. These belong to pre-existing work. The history implementation used as a foundation may not yet be committed. Inspect current state at session start; do not assume HEAD contains the reviewed functionality.

### Next-session instruction

> Read `docs/plans/claude-safina-workflow.md` and applicable repository instructions. Inspect existing working-tree changes, preserve unrelated work, and implement the first incomplete milestone. Update this plan with completed tasks, validation results, design adjustments, and the exact next action. Do not mark a milestone complete until its acceptance scenario passes.

At each session end record changed files/commits, tests run, unresolved failures, and the next concrete task. If implementation reveals a material conflict with an agreed decision, explain it before changing the decision. Routine implementation details can be resolved autonomously and documented.

### Progress log

- 2026-11-07: Saved the revised plan and all interactive product decisions. No feature code changed. Documentation-only validation; application tests not run.

### M0 session — 2026-11-07

- Completed the first incomplete milestone, M0, as a documentation increment. Read the plan, searched repository/ancestor instructions (no `AGENTS.md` found), inspected status/diffs and existing logbook, edit, scaling, restart, rollback and readiness implementations.
- Added `docs/plans/claude-safina-contracts-v1.md`; updated only this plan otherwise. Recorded baseline HEAD and ownership of existing tracked/untracked work. No staging, commits, resets, or application changes.
- Defined versioned journal/domain/tool contracts, separate investigation/operator capabilities, package dependencies, atomic selection/claim/intent, idempotency, crash recovery and stable logbook delivery. Existing fail-open history append cannot serve as the broker authorization gate.
- Inspection correction: manual edit review already scrolls; preserve it and add a proposal-specific complete review surface in M2. No product decision changed.
- Validation: `pnpm install --frozen-lockfile` passed (already up to date). `make verify-all` passed (exit 0): lint configuration, module tidy diff/verification, formatting diff, Go lint (0 issues), all Go race tests, Go build, website ESLint, Astro diagnostics (22 files; 0 errors/warnings/hints), and production build (2 pages). Existing Go test cache was used where applicable; no live cluster was contacted. `git diff --check` passed. Final SHA-256 comparison of all 290 initially tracked/untracked files confirmed only this plan changed; the contracts document is the only added file. No unresolved check failures. Full check output is available for this session at `/tmp/safina-m0-verify.log`.
- Exact next action: implement M1's versioned domain/store with exclusive writer locking, durable append/replay, idempotent submission and group transitions, then on-demand broker socket lifecycle/status/stop and durable logbook delivery. Keep execution unreachable through the investigation API. M2–M4 remain unimplemented; no live-cluster or Claude end-to-end validation is claimed.

### M1 session — 2026-11-07

- Implemented `internal/investigation/` and its store: typed alternatives, immutable revisions, alternative groups, decisions, single-use claims, versioned JSONL, exclusive writer locking, durable receipts, scoped submissions, recovery and pending delivery. Unknown/corrupt complete records block opening; truncated tails are preserved separately and surfaced. Failed/uncertain sync poisons the writer and never grants an execution claim.
- Added `internal/broker/`, `internal/broker/api/`, and `internal/localstate/`: detached on-demand startup, readiness/version/config checks, concurrent-start lock, owner-only Unix socket, Linux/macOS peer UID checks, strict request fields, separate status/stop lifecycle endpoints, reconnect and three investigation-only methods. No Kubernetes mutation or operator approval endpoint exists.
- Added `cmd/safina/broker.go` and registered `serve` plus `broker status/stop`. Updated the Makefile and README build command to compile the CLI package, since it now has more than one non-test source file.
- Added optional history linkage fields and `AppendDurable` plus a standalone configured redactor, keeping existing manual fail-open append behavior. Disabled/unavailable logbook storage does not disable investigation redaction; invalid patterns block startup. Stable event files use the existing query/export format and quota lock; pending delivery survives restart and an acknowledgement loss does not duplicate records.
- Focused tests cover concurrent independent-process startup, repeated/concurrent submissions, multiple sessions, broker SIGKILL/reconnect with pending proposals and no TUI, exclusive locking, permission checks, rejected approval/override fields and methods, zero requests to a fake Kubernetes HTTP endpoint, immutable edits, competing claims, failed sync, truncated/corrupt journals, accepted/unknown recovery, outbox failures, redaction/idempotency conflicts, correct outcome linkage across groups, and legacy history export compatibility. Focused race tests passed before final repository verification.
- Scope boundaries: M1 exposes the pending core through the Go client/local protocol; `safina mcp`, evidence reads/history briefs and the plugin remain M3/M4. M2 must prepare/revalidate reviews and build the operator UI before using internal selection primitives. Claims are removed from investigation responses. No cluster execution or application recovery is claimed.
- Preservation: initial SHA-256 inventory recorded in `/tmp/safina-m1-baseline.json`. Intentional existing-file edits are limited to Makefile, README, CLI registration, additive history fields and plan/contracts. Concurrent changes appeared in website files and, at final inventory, `internal/tui/components/modal.go`, `internal/tui/theme/theme.go`, and `internal/tui/view.go`; they are unrelated and were left untouched. Verification results describe the shared tree at check time; these concurrent edits are not M1-owned changes. No staging, commits or resets.
- Validation results: final `make verify-all` passed (exit 0): module/format checks, Go lint (0 issues), all Go race tests, Go build, website ESLint, Astro diagnostics (0 errors/warnings/hints), and production build (2 pages). macOS ARM64 cross-build passed; this is compile coverage, not a macOS runtime test. Compiled CLI smoke passed for serve/status/start/submit/stop/restart/get/idempotent retry using an unreachable cluster address and temporary state. `git diff --check` passed. Log: `/tmp/safina-m1-final-verify.log`. Intermediate source-changing builds hit a `strconv` import error; reruns passed after source settled. Final additions also triggered shadow/preallocation lint findings, which were fixed before the passing run. No unresolved failures; no live-cluster or real Claude workflow test belongs to this increment.
- M1 acceptance passed: concurrent/retried submissions retain one group, an independent broker restores pending work after SIGKILL, and investigation requests make zero calls to the fake Kubernetes HTTP endpoint. Only internal store primitives can select/claim; no operator route or Kubernetes executor exists yet.
- Exact next action: M2 Incoming proposals and broker-prepared scaling review, including complete diffs, durable final approval, UID/resourceVersion preconditions, controller warnings/overrides, broker-owned execution/monitoring and read-only uncertain-write reconciliation.

## References

- [Official Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk)
- [Claude Code plugins](https://code.claude.com/docs/en/plugins)
- [Claude Code MCP support](https://code.claude.com/docs/en/mcp)
- [Kubernetes RBAC](https://kubernetes.io/docs/reference/access-authn-authz/rbac/)

Confirm current SDK and plugin configuration syntax when implementing; product decisions above remain the source of truth.


### M2 session — 2026-11-07

- Completed Incoming proposals and broker-owned reviewed Deployment scaling.
  The TUI command palette now includes Incoming proposals and the workspace
  shows a pending count. A full scrollable surface provides summary, complete
  executable diff, explicit evidence coverage, immutable replica edits,
  rejection, controller override acknowledgement and separate final approval.
  Reopening a selected proposal shows durable decisions and outcomes. ANSI,
  control and bidi characters are removed from untrusted presentation text;
  narrow wrapping, end-of-diff access and stale asynchronous replies are tested.
- Added separate `operator` wire types/client and `/v1/operator` dispatch. The
  investigation endpoint/client remains start/submit/get only; operator fields
  and methods cannot be routed through it. Prepared reviews bind cluster, UID,
  resourceVersion, generation, exact replica change, warnings and digest; they
  expire after 15 minutes and require refresh after restart or drift. No-op
  approval is blocked; scale-to-zero effects are explicit.
- Approval uses a single kubeconfig snapshot, revalidates live state/controller
  observations, durably records selection/intent and delivers it to the logbook
  before issuing one JSON Patch with atomic UID and resourceVersion tests.
  Client-go Retry-After retries and HTTP redirects are disabled for the broker
  client. HPA/GitOps warnings require exact logged overrides; unavailable HPA
  discovery requires a coverage override. Separate-resource HPA changes cannot
  be atomically guarded with the Deployment patch; possible controller reversal
  remains explicit. Kubernetes may also apply admission/defaulting behavior.
- Persisted decisions include the complete prepared review. Logbook records now
  link decisions/outcomes and recovered interruptions to their exact target,
  proposal/revision, operation, operator, overrides and reviewed replica values.
  Durable storage or pre-send logbook failure prevents the request. Post-send
  recording failure cannot grant another send.
- Broker monitoring runs in a cancellable worker independent of the TUI and
  resumes accepted operations after restart. It checks accepted UID/generation,
  controller observation, updated/available/total replicas and deadline failure;
  replacement/later generations supersede the operation. Read failures expose
  incomplete monitoring. Recovered intents and ambiguous responses reconcile
  through reads only and remain causally unknown even when replicas match.
- Acceptance passed in `TestIncomingApproveCloseReopenObservesBrokerOutcome`:
  the actual TUI key/update handlers and Unix operator API review 2 → 3, require
  diff/final approval, send once, close the TUI context before readiness, and
  reopen to the broker's completed record. The HTTP fixture controls API
  acceptance and subsequent controller status independently. This is automated
  integration coverage, not a live Kubernetes or interactive terminal trial.
- Additional tests cover competing sibling approvals, duplicate approval,
  immutable editing/rejection, zero/no-op/range checks, UID/version drift and
  the final-fetch race, kubeconfig retargeting, HPA/GitOps overrides, logbook and
  journal failures, lost response, Retry-After ambiguity, accepted/intent recovery,
  no replay, readiness failure/supersession and linked history delivery. Existing
  independent-process startup/crash tests and manual UI/history tests also pass.
- Implementation ownership: added `internal/investigation/review.go`,
  `internal/broker/{review,execution,execution_test,operator_client}.go`,
  `internal/broker/operator/api.go`, and `internal/tui/incoming{,_test}.go`.
  Updated broker client/server/cluster, domain types/store, TUI model/update/view,
  CLI model wiring, README and plan/contracts. Preparation remains in broker
  files for this increment rather than introducing `internal/proposal` before
  multiple actions need shared logic; the approval boundary is unchanged.
- Preservation: initial hashes are in `/tmp/safina-m2-baseline.json`. Concurrent
  changes appeared in `internal/tui/barbarossa_test.go`, `site/README.md`,
  `site/scripts/capture.sh`, `site/src/components/KeyList.astro`,
  `site/src/pages/index.astro`, and `site/src/styles/global.css`; they were left
  untouched. Other pre-existing working-tree work was preserved. No staging,
  commits, resets or real-cluster mutations were performed.
- Validation: focused broker/store/TUI race tests passed. Final `make verify-all`
  passed (exit 0): module verification/tidy diff, formatting, Go lint (0 issues),
  all Go race tests, Go build, website ESLint, Astro diagnostics (22 files;
  0 errors/warnings/hints), and production build (2 pages). Native CLI build and
  macOS ARM64 cross-build passed; no macOS runtime validation is claimed.
  `git diff --check` passed. Full log: `/tmp/safina-m2-final-verify.log`.
  Initial verification found a formatter blank-line issue; a subsequent lint
  pass found exhaustive-switch, if-chain style and fixture-path taint findings.
  All were addressed before the passing final run. No unresolved check failures.
- Limits/next action: M3 remains unimplemented. The evidence tab explicitly
  reports no captured evidence references; history briefs/matching previous
  attempts, bounded inspection/log/event/history tools, SDK stdio MCP, image
  changes, exact restart and pinned rollback come next. Implement the official
  Go MCP SDK adapter without exposing OperatorClient, then scoped evidence and
  history retrieval and the remaining three typed actions. Real-cluster and
  Claude/plugin release validation remain M4; scaling alone is not the release.


### M3 session — 2026-11-07

- Added `safina mcp` with official Go MCP SDK v1.8.0 and protocol-only stdout.
  The adapter accepts only `api.InvestigationClient`, with nine typed tools:
  contexts, start, workload inspection, events, logs, history, evidence, proposal
  submission and investigation retrieval. No approval/execution/override method
  is registered. SDK schemas and strict broker decoding reject extra fields.
  Local creation, submission and evidence capture are explicitly state-changing;
  none can mutate Kubernetes. SDK compatibility was checked against official
  release notes/examples, and stdio runs through a real subprocess test.
- Added versioned journal evidence captures with validated proposal references,
  pinned cluster/namespace/resource, observation time, truncation and coverage
  metadata. Captures are redacted before return/persistence, limited to 16 KiB
  each and 64 per investigation, and survive replay. Current redaction is reapplied
  when returning older evidence; byte limits are enforced after filtering too.
  Inspection places target identity/version before potentially truncated data,
  selects safe fields, and bounds related objects. Logs cap at 200 lines / 16 KiB;
  events and dependencies cap at 100. Permission failures and partial HPA
  discovery are explicit. `history.evidence: false` disables new persistence
  while allowing redacted live responses without stored IDs.
- Start now automatically returns a bounded 24-hour history brief (50 records /
  16 KiB), targeted to the selected resource/related workload records or pinned
  cluster. Deeper history lookup supports time and resource scope (100 records /
  16 KiB). Exact cluster equality is enforced after legacy alias matching.
  Historical evidence requires a retained record in the same cluster; Secret
  contents are excluded and expired evidence is explicit. Operator reviews show
  prior attempts of the same action kind, including failed and unknown outcomes.
- Generalized preparation/execution to image, restart and exact rollback.
  Images patch the reviewed regular container's image; restart uses its frozen
  reviewed annotation timestamp. Rollback resolves an owned ReplicaSet's exact
  revision/UID/version, revalidates it before execution, and sends its reviewed
  template with the generated pod-template hash label removed. Image/restart
  preserve unrelated fields. All four actions retain UID/resourceVersion guards,
  explicit controller overrides, durable pre-send recording, one-request claims
  and independent monitoring. No rollback resolves "previous" after approval.
- Raw executable patch bytes remain in the broker's in-memory review cache and
  never enter wire responses or the journal. Decisions persist action, digests,
  source revision metadata and a complete redacted diff. Literal environment
  values are masked, including unlabeled credentials, while changes remain
  visibly marked. Returned templates that differ from the reviewed digest are
  uncertain, not falsely completed. Recovered/ambiguous writes still never replay.
- Extended Incoming proposals for image (`container=image`) and rollback revision
  edits, captured evidence, previous attempts and all-action confirmation copy.
  Restart remains parameterless. Existing diff/final approval gates and manual
  workflows remain intact.
- Acceptance passed: an MCP SDK client submits each of scale, image, restart and
  rollback; the actual TUI key/update handlers load its review, show the diff,
  explicitly approve, close before controller readiness, reopen to the broker's
  completed outcome, and the MCP client retrieves that outcome. This is
  `TestIncomingApproveCloseReopenObservesBrokerOutcome`, now parameterized across
  all four actions. The HTTP fixture separates API acceptance from controller
  readiness. A separate MCP integration test covers inspection, history, evidence,
  unknown approval tools/fields, permission failures, scope isolation and all
  action outcomes. This is automated protocol/TUI integration, not a live cluster
  or real Claude session.
- Additional tests cover scoped evidence persistence/replay and invalid refs,
  history/output bounds, new redaction policy on old captures, capture opt-out,
  historical Secret exclusion, exact frozen restart, changed rollback source,
  unrelated field preservation, masked environment values, no raw patch journal
  payload, and TUI editing/evidence rendering. All earlier broker/store/manual UI
  regression tests remain passing.
- Full-suite execution exposed a detached-broker shutdown race: the socket could
  close before history cleanup completed. Startup now waits for runtime lock
  ownership to be released before launching a replacement; test cleanup waits
  for that same boundary. Added a regression that proves a new launch cannot
  occur while the previous runtime owner still holds the lock.
- New files: `cmd/safina/mcp{,_test}.go`, `internal/mcpserver/server.go`,
  `internal/broker/{actions,actions_test,reads,reads_test,presentation,mcp_test}.go`,
  `internal/broker/api/reads.go`, `internal/investigation/evidence.go`, and
  `internal/investigation/store/evidence.go`. Updated the investigation API/client,
  broker review/execution/dispatch and tests, durable domain/store, Incoming UI
  and tests, CLI registration, module manifests, README and plan/contracts.
- Preservation: hashes are in `/tmp/safina-m3-baseline.json`. Pre-existing Go and
  website work was preserved. Unrelated website changes/commits occurred during
  the session, including `site/README.md`, `site/public/og.jpg`,
  `site/scripts/og.html`, `site/src/components/{Aboard,Install,Opening}.astro`,
  `site/src/data/tour.ts`, `site/src/layouts/Base.astro`, and
  `site/src/pages/index.astro`; these were left untouched. Verification reflects
  the shared tree at check time. This session performed no staging, commits,
  resets or live-cluster mutations.
- Validation: final `make verify-all` passed (exit 0): module tidy diff and
  verification, formatting, Go lint (0 issues), all Go race tests, Go build,
  website ESLint, Astro diagnostics (0 errors/warnings/hints), and production
  build (2 pages). Native CLI and macOS ARM64 cross-build passed; this is not a
  macOS runtime test. The stdio subprocess and all-action TUI/MCP acceptance
  tests passed. `git diff --check` passed. Logs:
  `/tmp/safina-m3-final-verify.log`, `/tmp/safina-m3-acceptance.log`,
  `/tmp/safina-m3-regression.log`.
- Resolved intermediate failures: a digest parameter shadowed a helper during
  initial compilation; lint found formatting, shadowing, test-command provenance,
  explicit nonfatal coverage handling and string-building issues. One test
  fixture merged a replaced template instead of replacing it. Full verification
  then caught re-redaction expanding a truncated marker past its byte bound and
  the detached-process cleanup race described above. Fixes and regression tests
  preceded the final passing run. No unresolved check failures.
- Exact next action: M4. Add the Claude plugin with MCP configuration and a
  history-first `/safina:investigate` skill, verify current Claude/plugin syntax,
  document evidence disclosure/storage/security limits, then run a disposable
  cluster trial with separate Claude and Safina terminals across all four actions.
  Real-cluster/controller/admission behavior and real-Claude handoff remain
  unvalidated; M3 completion does not declare the release complete.


### M4 session — 2026-11-07

- Added `plugins/safina/.claude-plugin/plugin.json`, `.mcp.json`,
  `skills/investigate/SKILL.md`, and plugin README. The namespaced
  `/safina:investigate` workflow starts with explicit cluster selection and
  retained history, captures scoped live evidence, submits typed alternatives,
  hands approval to the operator, and retrieves the actual selected revision and
  outcome. It distinguishes stale agent verification prose after operator edits
  from the authoritative typed action/diff. No hooks, shell tools, permission
  grants, operator routes, or execution capabilities were added to the plugin.
- Checked current packaging syntax against official Claude plugin manifest and
  skills documentation. Claude Code 2.1.319 loaded the plugin and skill and
  connected its MCP server. `claude plugin validate --strict plugins/safina`
  passed. The catalog-only icon warning and marketplace-install advice are
  nonblocking for this local plugin; nothing was published. The Codex
  skill-creator validator rejected Claude's supported `argument-hint` field;
  Claude's native validator is authoritative for this package.
- Documented setup, on-demand broker startup, explicit stop/upgrade, storage,
  evidence bounds and model-provider disclosure, redaction/retention, supported
  actions, recovery limits, and same-user security boundaries. Added
  [a reproducible release trial](claude-safina-release-trial.md) and updated the
  root README/contracts. TUI now accepts `--runtime-dir`, `--state-dir` and
  `--history-dir`; MCP now accepts `--history-dir`, enabling isolated journal and
  logbook paths without modifying HOME or the normal kubeconfig.
- Real acceptance passed on minikube profile `safina-m4-20261107`, Kubernetes
  1.37.2, Docker driver. Claude read a clearly labeled historical setup fixture
  (not a claimed prior repair), current Deployment/Pod/controller evidence,
  events and logs. It submitted scale-to-3 and restart as explicit trial
  alternatives in investigation `38e42c5f8740187faf60dd077032fb36`.
- A separate 80×24 Safina PTY opened Incoming proposals, revised scale to 4,
  inspected the regenerated complete diff and approved with `a`, then `y`.
  Revision 2 was selected, revision 1 superseded and restart made unavailable.
  Operation `96bb7f9541da2120dd8e0a54b087cf76` completed with generation 3 and
  4/4 replicas available. After closing the TUI, the same real Claude session
  retrieved the outcome, linked history and fresh evidence, explicitly reporting
  application recovery as unverified. Reopened Safina displayed the completed
  outcome. There were zero pending logbook deliveries before shutdown.
- Trial qualification: Claude ran in print/resume mode with the skill and Safina
  MCP tools, no permission bypass; operator input was automated PTY keystrokes,
  not a human usability study. Live execution covered scale. All four actions
  passed the existing HTTP-backed MCP → TUI approval → broker outcome tests;
  live image/restart/rollback and arbitrary admission/controller combinations
  are not claimed. The temporary broker was stopped and its cluster deleted;
  no production cluster was contacted. Artifacts are under
  `/tmp/safina-m4-trial`; see the release-trial document for exact IDs and limits.
- The trial exposed edit/confirmation text falling below a long summary on a
  small terminal. Moved edit, approve and reject prompts before the summary,
  resetting the viewport on entry. Added
  `TestIncomingPromptsRemainVisibleAboveLongSummary`; focused Incoming race
  tests including all four action acceptance cases passed. The reopened final
  binary also displayed the persisted scale outcome.
- Validation: final `make verify-all` passed (exit 0): module checks, formatting,
  Go lint (0 issues), all Go race tests, Go build, website ESLint, Astro diagnostics
  and two-page production build. Native and macOS ARM64 builds passed; no macOS
  runtime test is claimed. Plugin validation and `git diff --check` passed.
  Logs: `/tmp/safina-m4-final-verify.log`, `/tmp/safina-m4-tui.log`, and
  `/tmp/safina-m4-plugin-validation.log`. An earlier full verification also
  passed before the prompt visibility fix. No unresolved repository check failure.
- Setup limits resolved: the first Claude smoke call preceded kubeconfig creation
  and returned unavailable; subsequent calls succeeded. Minikube warned about
  its version support list and external registry connectivity, but initialized.
  The first image pull failed; loading the host's nginx image made the fixture
  ready before proposals. These were setup failures, not incident-recovery claims.
- Preservation: baseline hashes are `/tmp/safina-m4-baseline.json`. Intentional
  changes are the plugin, trial/plan/contracts docs, root README, CLI directory
  flags and Incoming prompt layout/regression test. Concurrent unrelated changes
  appeared in `site/README.md`, `site/src/pages/index.astro` and new
  `site/src/components/Questions.astro`; these were left untouched. No staging,
  commits, resets or marketplace/account configuration changes were performed.
- Next action: no incomplete milestone remains in this plan. Review the local
  changes for release. Publishing/distribution, expanded live action/platform
  coverage and retention compaction are separate follow-up work, not silently
  included in this acceptance result.


### Adversarial QA follow-up — 2026-11-07

- User requested stress testing against minikube, fixes for discovered bugs and
  a final report. Full findings, live test matrix, reproductions and limitations:
  [adversarial QA report](claude-safina-qa.md).
- Fixed invisible/control-character image proposals that Kubernetes accepted
  while terminal sanitization could hide the character. New parameters reject
  Unicode whitespace/control/format characters; preparation revalidates older
  saved proposals. Live diffs now show escaped invisible/control characters.
  Confirmed against the real cluster and through a fresh Claude MCP session.
- Fixed large-history inbox timeouts: store index contains only listing data,
  complete selected investigations load on demand, and the monitor snapshots
  only accepted/unknown operations. With 72 retained 240 KB summaries and eight
  of them carrying proposals, the inbox remained approximately 25 KB / 3 ms on
  this machine; full selected summary/evidence remained available. This is a
  measured result, not a general latency guarantee.
- Fixed stale inbox errors and response/selection races: connection errors clear
  independently of review errors; inbox epochs reject old replies; cursor follows
  proposal identity; detailed responses must match the current request/selection.
  Outcome polling and all four TUI approve/close/reopen paths remain working.
- Live coverage included all four actions and exact field preservation, 32
  concurrent sibling/duplicate approvals, stale resourceVersion, replacement UID,
  HPA policy drift/override, GitOps override, failed and paused rollouts, scale to
  zero, RBAC denial, cross-investigation evidence rejection, 80 concurrent capture
  attempts bounded to 64, recording failure/recovery, and a proxy that dropped a
  real accepted PATCH response. The ambiguous write stayed unknown with exactly
  one proxy-observed PATCH across duplicate approval and broker SIGKILL/restart.
  A separately accepted scale resumed monitoring after SIGKILL and completed
  without another generation increment; its old review could not execute again.
- Claude CLI independently reviewed the load-fix design and ran the real MCP
  hidden-character regression. The focused MCP run used an isolated workspace,
  explicit server configuration and no built-in execution tools or permission
  bypass. Findings were checked against source and live behavior before changes.
- Validation: final `make verify-all` passed (exit 0), including module/format
  checks, lint (0 issues), all Go race tests/build, website lint, Astro diagnostics
  and two-page production build. Native and macOS ARM64 builds passed; no macOS
  runtime test is claimed. `git diff --check` passed. Logs and local probe scripts
  are under `/tmp/safina-qa`; final verification is `verify-final2.log`.
- Added regression tests for hidden parameters/diff rendering, lightweight index
  and active monitoring ownership, stale inbox responses, stable selection and
  network-error recovery, and mismatched detail responses. Updated all-action
  TUI acceptance tests to await selected detail after reopening.
- Intermediate failures: corrected test fixture deadline/proxy-query assumptions,
  test import and asynchronous-detail handling, plus formatter/shadow/struct-tag
  findings during implementation. A live controller update correctly invalidated
  one restart review; a fresh explicit test review completed. No automatic stale
  write retry was introduced. No unresolved repository check failures remain.
- Ownership: changed broker review/execution/operator client/types, domain action
  validation, store listing/monitor snapshots and tests, Incoming TUI/update and
  tests, and these QA/plan documents. Baseline hashes are
  `/tmp/safina-qa-baseline.json`. Concurrent website edits, including
  `site/src/components/Questions.astro` and existing site files, were preserved.
  No staging, commits, resets, publication or production writes occurred.
- The disposable broker was stopped after testing; its minikube profile is
  removed as QA cleanup. Local diagnostic artifacts are retained for review.
  M0–M4 remain complete. Next action is release review; broader platform or
  deployment-policy coverage remains follow-up work rather than an implied
  guarantee from these finite tests.

### Second failure-probe round — 2026-11-07

- No incomplete implementation milestone remains. User-requested follow-up QA
  found and fixed five additional failure modes: slow inbox response starvation,
  detail errors leaving the TUI busy, oversized derived logbook events blocking
  later delivery, slow/unordered delivery blocking state access, and near-limit
  intent recovery preventing journal reopening.
- Added focused failing-then-passing regressions plus compact-record corruption,
  ordered concurrent delivery and a Unix broker test for individually bounded
  requests producing an oversized history record. No mutation is issued by that
  rejection test. See the [follow-up report](claude-safina-qa-followup.md).
- A focused read-only Claude CLI review found three of these defects; each was
  independently reproduced before edits. Restricted file scope, Read/Grep tools,
  isolated working directory and disabled MCP servers kept review context small.
- Compatibility smoke replayed a copy of the previous 23 MB journal twice,
  preserving all 106 investigations' summaries, proposals and decisions. Test
  brokers used only an isolated localhost endpoint and stopped cleanly. Old
  snapshots remain readable; older binaries cannot read newly written compact
  execution updates. The contract and QA report document that downgrade limit.
- Existing website, AGENTS.md and other concurrent edits were preserved. This
  round used local fixtures and did not repeat the earlier live-cluster trial.
- Final `make verify-all` passed (exit 0), including zero lint issues, all Go race
  tests/build and website lint/diagnostics/build. Native compatibility smoke and
  macOS ARM64 compilation succeeded; no macOS execution is claimed. Formatter
  and test-only lint findings from intermediate runs were corrected. Final log:
  `/tmp/safina-qa2/verify-final2.log`; `git diff --check` passed.

### Evidence and interrupted-write probes — 2026-11-07

- Additional user-requested probes found two evidence bugs: invalid UTF-8 in long
  logs discarded useful trailing text, and mid-stream HTTP failure discarded
  already received log lines. Normalize binary text before byte bounding; retain
  interrupted log observations through redacted capture with truncation and
  incomplete-coverage warnings. Both have failing-before/fixed-after reproducers.
- Warning-slice ownership already passed its probe. Added torn compact acceptance
  probes at three byte boundaries to check preserved tails, unknown recovery,
  no new execution claim and repeated reopen. See the
  [evidence QA report](claude-safina-qa-evidence.md).
- Claude CLI supplied a focused read-only two-file review; findings were verified
  independently. Existing public error sanitization disproved its speculative
  transport-error disclosure concern. No broad context or extra MCP servers were
  loaded. No live Kubernetes trial was repeated in this round.
- Final `make verify-all` passed (exit 0): module/format checks, zero lint issues,
  all Go race tests/build and website lint/diagnostics/production build. Torn
  acceptance recovery passed all three boundaries. `git diff --check` passed.
  Logs: `/tmp/safina-qa3/verify-final.log`. One intermediate test-style lint
  finding was corrected. M0–M4 remain complete; unrelated work is preserved.

### Broker lifecycle probes — 2026-11-07

- Additional probes reproduced three defects: FIFO state/log paths could block
  before file-type validation, oversized typed requests unnecessarily started a
  broker and returned a misleading protocol error, and a 10-second server write
  timeout discarded successful replies within the client's 15-second deadline.
- Open state files nonblocking until regular-file validation; preflight the full
  request envelope against the shared 256 KiB bound before startup; allow server
  response writes 20 seconds while preserving the 15-second client timeout.
  Idempotency and explicit approval semantics remain unchanged.
- Cancellation waiting for startup/runtime locks passed without a fix: no launch,
  socket creation or cluster calls. Claude CLI's focused two-file read-only
  review supplied the request-size/deadline probes, independently reproduced.
  Details: [lifecycle QA report](claude-safina-qa-lifecycle.md). No live cluster
  trial was repeated in this round; unrelated working-tree changes are preserved.
- Focused regressions passed with the race detector, followed by full
  `make verify-all` (exit 0): module/format checks, zero lint issues, all Go race
  tests/build and website lint/diagnostics/build. macOS ARM64 cross-compilation
  passed; no macOS runtime test is claimed. `git diff --check` passed.
  Logs: `/tmp/safina-qa4/verify.log`. M0–M4 remain complete.
