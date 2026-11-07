# Adversarial QA: Claude / Safina workflow

Date: 2026-11-07. Scope: the M0–M4 investigation, proposal, operator review,
execution and outcome workflow. Tests used the disposable minikube profile
`safina-qa-20261107` (Kubernetes 1.37.2, Docker driver), explicit kubeconfig and
isolated runtime/state/history under `/tmp/safina-qa`. No production context was
used. Existing unrelated working-tree work was preserved.

## Bugs reproduced and fixed

### 1. Invisible image characters changed what review appeared to approve

Before the fix, a proposal for `nginx:1.27-\u202ealpine` (an actual U+202E
character, not six literal escape characters) passed domain validation and was
accepted by the real Kubernetes API. Terminal sanitization removed the format
character, making the displayed image misleading. The test Deployment was
`qa/control-image`; this was an intentional failure injection.

- Image/container parameters now reject Unicode whitespace, control characters,
  and format characters such as bidi overrides and zero-width characters.
- Preparation validates the action again, so a pending proposal persisted by an
  older binary cannot bypass the new validation after upgrade.
- Complete diffs visibly escape control/format characters already present in
  live templates, rather than silently removing those bytes. Agent prose remains
  sanitized; executable identifiers cannot use invisible characters.
- Live retest rejected U+202E, U+200B, ANSI escapes, NUL and NBSP. A pre-fix saved
  proposal was also rejected at preparation. A fresh real Claude CLI session
  submitted U+202E through MCP, received `invalid_argument`, and confirmed that
  no proposal, decision or execution was stored.
- Regression tests: `TestImageActionRejectsInvisibleParameters` and
  `TestIncomingDiffRevealsInvisibleLiveValues`.

### 2. Large investigations overloaded inbox refresh and monitoring

The old inbox cloned and re-redacted every complete investigation, including
all saved summaries, evidence, rationale, reviews and outcomes. The monitoring
loop cloned every investigation once per second, even when nothing was running.
Concurrent submissions of 240 KB summaries produced inbox request timeouts/EOFs
in the real TUI; old outstanding requests also delayed shutdown.

- The store now supplies a lightweight index for the operator inbox. It includes
  every proposal revision and the fields needed to identify and select it, with
  stable creation/ID ordering. Investigations with no proposals need no inbox row.
- Full selected detail is fetched through the existing redacted investigation
  getter. Evidence, rationale and outcomes are retained, not discarded or
  silently truncated. Review, revision, rejection and outcome screens use the
  selected detail; outcome polling still refreshes it.
- The monitor only snapshots investigations with accepted or unknown operations.
  Unknown writes remain read-only reconciliation work; idle history is skipped.
- After the fix, 72 large investigations were retained with stable retry keys.
  The inbox was 15,187 bytes and took about 2 ms locally. Adding proposals to
  eight of those large investigations yielded a 25,165-byte inbox in about 3 ms.
  Fetching one full 240,008-byte summary with its proposal/evidence took about
  91 ms. These are observations from this machine, not performance guarantees.
- The fixed TUI displayed the populated inbox, full selected rationale, complete
  diff and captured evidence. All four automated TUI approval/reopen scenarios
  also passed with on-demand detail loading.
- Regression: `TestIndexOmitsHeavyHistoryAndActiveKeepsRecoveryWork` verifies
  bounded index content, detached copies, full retained detail and accepted/
  unknown monitoring selection. Existing all-action TUI acceptance tests now
  exercise asynchronous detail retrieval on reopen.

### 3. Inbox refresh could retain errors or overwrite newer selection state

During the load test, a successful refresh could leave a stale network error on
screen. Review errors and connection errors shared the same field. Inbox calls
also had no response epoch, so an older slow reply could overwrite a newer
snapshot, and insertion before the cursor could change the selected row.

- Connection errors now clear on successful refresh without clearing review or
  approval errors.
- Inbox responses carry request epochs; outdated replies are ignored.
- Selection follows investigation/proposal/revision identity across refreshes.
- Full-detail responses must match the selected investigation and request epoch.
- Regressions: `TestIncomingInboxRefreshKeepsSelectionAndClearsOnlyNetworkError`
  and `TestIncomingDetailIgnoresWrongSelection`.

## Live test matrix

The Python test clients used the real Unix investigation/operator protocols and
real Kubernetes APIs. Operator protocol calls were deliberate QA approval input,
not additional MCP capabilities. The lost-response proxy forwarded to the real
TLS API and dropped only the PATCH response after acceptance. TUI tests used a
separate real terminal process; automated Go acceptance tests additionally drive
its actual key/update handlers for all four actions.

| Scenario | Result |
| --- | --- |
| Scale | Completed on live cluster; pod template preserved |
| Image change | Completed; only named container image changed |
| Restart | Completed; exact frozen annotation preserved, other fields unchanged |
| Pinned rollback | Completed; exact owned ReplicaSet template restored, generated hash label removed |
| Repeated approvals / siblings | 32 concurrent calls produced one decision, one operation and one generation increment |
| No-ops | Scale, image and rollback approvals rejected |
| Drift | Old review rejected after external metadata change; refresh exposed drift |
| Replacement | Same name with a new UID rejected at both approval and preparation |
| HPA | Missing override rejected; changing HPA policy invalidated review; refreshed explicit override accepted |
| GitOps | Restart required recorded explicit override and then completed |
| Failed image rollout | Real controller reported ProgressDeadlineExceeded; broker recorded failed |
| Paused rollout | Did not falsely complete; subsequent resume generation superseded the operation |
| Scale to zero | Warning present; controller reached zero desired replicas and recorded completion |
| Evidence isolation | Foreign-investigation evidence rejected; literal environment value absent from inspection |
| Capture stress | 80 concurrent reads produced exactly 64 unique captures and 16 explicit failures |
| Capability boundary | Investigation route rejected approve and injected approval fields |
| Restricted RBAC | Missing HPA coverage warned; forbidden patch failed without mutation; logs/events reported permission gaps |
| Recording failure | Replacing the isolated history actions directory with a file blocked the reviewed write; restored recording allowed a newly reviewed action |
| Ambiguous write | Real API accepted one PATCH; dropped response remained unknown, matching live state did not become causal success |
| Broker SIGKILL/restart | Ambiguous outcome recovered as unknown with one proxy-counted PATCH; a separately accepted scale resumed monitoring and completed without another generation increment; old reviews rejected |
| Large retained history | Fixed inbox stayed small and responsive; selected full evidence/prose remained retrievable |
| Real Claude MCP | Hidden-character proposal rejected; no stored proposal; history and scope checks succeeded |

After the fixes, all four live actions were exercised again with exact field
preservation assertions. A real status update invalidated one restart review;
the request was correctly rejected without automatic retry. A fresh test review
subsequently succeeded. Rollback used revision 4 as observed on the live owned
ReplicaSet, not an inferred “previous” revision.

## Validation, artifacts and limitations

- Focused race tests cover broker, store, domain and TUI regressions.
- Final `make verify-all` passed: module and formatting checks, lint (0 issues),
  all Go race tests, Go build, website ESLint, Astro diagnostics and the two-page
  production build. Log: `/tmp/safina-qa/verify-final2.log`. Native and macOS ARM64
  builds passed; `git diff --check` passed.
- Claude CLI independently reviewed the load-fix design and exercised the MCP
  regression. The latter ran in an isolated working directory, with an explicit
  system prompt, only the Safina MCP server, no built-in execution tools and no
  permission bypass. This kept the task focused and avoided loading unrelated
  repository/server context.
- Local artifacts: `/tmp/safina-qa/{actions.log,stress.log,edge.log,rbac.log,
  failed-retest.log,fault-retest.log,recording.log,actions-fixed.log,
  claude-load-review.json,claude-mcp-regression.json,accepted-recovery.log,regression4.log}` plus the
  isolated journal/logbook and verification logs. Temporary scripts in that
  directory contain the exact probes. Treat kubeconfigs, proxy TLS material and
  Claude output as local artifacts, not files to publish.
- Initial test-harness issues were corrected: an overly short deadline expired
  during fixture setup, and the proxy assertion initially omitted client-go's
  `timeout=6s` query parameter. These were not Safina bugs. Verification also
  caught formatting, shadowing and malformed struct tags during implementation;
  those were corrected before final validation.
- This is adversarial coverage of the listed cases, not a proof against every
  failure mode. It does not establish application recovery, test macOS runtime
  behavior, or create a sandbox against arbitrary same-user tools. All remote
  writes were limited to the disposable cluster.
