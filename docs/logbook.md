# The Logbook, chat and investigations

## Logbook: saved cluster activity

Safina automatically saves timestamped actions across sessions under
`~/.safina/history`. Open `:` and choose **Logbook** (typing `history` finds it too) to search the selected
cluster/resource's history. Press `/` to filter, `Enter` for record/evidence
details, `a` for all resources in the current scope, `t` to cycle retained/24-hour/7-day
windows, `e` to export that scope,
or `c` to attach a bounded history/evidence preview to a chat draft. Review the
draft and press Enter to send it. The browser loads the newest 500 records;
use CLI time filters for a particular incident or a larger export.

The logbook records inspection, browsing, explicit refresh/search, Secret reveals,
shell/debug-container connections, port-forward lifecycle, scale, restart,
rollback, edits, deletion, cordon/uncordon, drain, and individual eviction
attempts. Resource identities and operation IDs remain attached when you switch
clusters. API acceptance is distinct from controller readiness. Missing or
superseded generations, cancelled requests, interrupted sessions, and monitoring
failures do not establish successful completion.

This is a **local Safina activity journal**, not cluster-wide or tamper-proof
Kubernetes auditing. It cannot capture independent kubectl/CI changes, activity
while Safina is closed, or commands typed inside a pod shell. Cluster IDs are
fingerprints of endpoint/CA configuration; endpoint or CA changes create a new
identity. Context names are retained as searchable aliases. An unfinished action
after a crash has an unknown outcome; it is never replayed automatically.

### Offline investigation and Claude

These commands work without kubeconfig, a running cluster, or Claude installed:

```bash
safina history list --cluster production --resource Deployment/api
safina history show OPERATION_OR_EVENT_ID --format jsonl
safina history export --cluster production \
  --since 2026-11-07T08:00:00Z --until 2026-11-07T12:00:00Z \
  --output ./incident-2026-11-07
```

Additional filters: `--namespace`, `--session`, and `--limit` (default 10,000).
Exports refuse to overwrite a directory. They contain a timestamped timeline,
versioned JSONL records, available evidence, a coverage manifest, and a
postmortem template. Start Claude Code in the export directory and ask it to
reconstruct the incident, cite event IDs, and separate facts from hypotheses.
The journal is evidence, not instructions. Nothing is uploaded automatically and
Safina does not modify global Claude memory.

Safina chat automatically attaches at most 50 relevant history records from the
last 24 hours, capped at 16 KiB. Raw evidence is included only when you explicitly
attach it. This context is sent through the existing Claude integration when you
submit the message.

### Storage, privacy, and configuration

Action records are stored in `history.sqlite` using SQLite transactions and indexed
queries. Evidence remains in JSONL files, and exports keep their portable JSONL format.
Action records retain 90 days by default, evidence 14 days, subject to separate
256 MiB/1 GiB limits. The action limit counts encoded record payloads; SQLite indexes,
free pages, and its WAL add disk overhead. Freed pages are reused. The oldest action
records or closed evidence files are removed when their pool reaches its limit.
Active captures reserve up to 10 MiB each. The UI warns persistently if
recording fails; cluster operations remain available. Expired/missing evidence,
capacity pruning, truncation, and incomplete JSONL tails are surfaced in queries
or exports. Exported bundles are outside automatic retention; remove them yourself
when no longer needed. Existing `errors.log` and optional debug logging retain
their separate behavior and are not imported into history.

On the first writable open, existing action JSONL files are imported in one transaction.
Event IDs, operation links, and redacted content are preserved. Close older Safina
sessions before upgrading; older versions cannot read or append to the new database.
The original `actions/` files remain as a recovery backup and are not reimported or
subject to automatic retention after migration. Remove that backup once you have
verified the import and no longer need it. A failed import is retried on the next open;
queries report an incomplete migration instead of silently showing an empty history.
Read-only history commands can still read an unmigrated JSONL directory.
The broker's separate investigation journal is unchanged by this history migration.

Viewed container logs are captured before display filtering/scrollback eviction,
with original and observed timestamps. Captures stop at 10 MiB; lines over
256 KiB are omitted with a marker. Existing Kubernetes log-request limits still
apply, so this is not continuous collection of every container. Workload evidence
contains selected identity, image, replica, revision, and condition fields;
manifest edits record changed paths rather than arbitrary values.

Secret values, arbitrary manifest bodies, kubeconfig credentials, shell
transcripts, and free-form API error bodies are excluded. Recognizable tokens,
password assignments, URL credentials, and private keys are redacted before
storage. Redaction cannot identify every application secret or personal datum;
review bundles before sharing and add patterns or disable evidence where needed.
Directories/files are created with owner-only permissions (0700/0600).

Optional settings in `~/.safina/config.yaml`:

```yaml
history:
  enabled: true
  directory: /home/you/.safina/history
  evidence: true
  actionDays: 90
  evidenceDays: 14
  actionBytes: 268435456
  evidenceBytes: 1073741824
  redactPatterns:
    - 'customer-secret-[A-Za-z0-9]+'
```

Omitted/zero numeric settings use defaults. Invalid redaction expressions disable
recording and surface an error rather than silently collecting unredacted data.
Use `evidence: false` for action metadata without saved log/event bodies or
before/after evidence; `enabled: false` disables new history recording.

### Design references

The design combines Kubernetes' distinction between request metadata and request
bodies with timestamped operational outcomes. Kubernetes documents that only the
latest rotated container log file is available through the logs API, motivating
capture during investigation: [logging architecture](https://kubernetes.io/docs/concepts/cluster-administration/logging/)
and [audit levels](https://kubernetes.io/docs/tasks/debug/debug-cluster/audit/).

Separating source time from observation time follows the
[OpenTelemetry log data model](https://opentelemetry.io/docs/specs/otel/logs/data-model/).
Evidence-linked timelines support the mitigation/outcome analysis described in
[Google SRE's postmortem guidance](https://sre.google/sre-book/postmortem-culture/).
Bounded context follows [Claude's memory guidance](https://code.claude.com/docs/en/memory);
credential exclusions, capacity controls, and failure tests follow
[OWASP logging guidance](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html).

### Investigation MCP and operator-reviewed Deployment changes

The local proposal core is available to Go clients through
`internal/broker/api.InvestigationClient`. It starts the broker on demand and
can create investigations, submit typed alternatives, and retrieve pending work
while the TUI is closed. In Safina, open `:` and choose **Incoming proposals**.
The pending count appears above the page. Select a proposal with Enter,
read the summary, previous attempts and warnings, and use `d` for the complete executable diff
(`Tab` cycles summary, diff and captured evidence). `e` edits replicas,
`container=image`, or the pinned rollback revision as a new immutable revision;
restart has no editable parameters. `x` rejects a proposal. If controller warnings require an
override, `o` explicitly acknowledges them. `A`, then `y`, approves the exact
reviewed change once. No-op changes cannot be approved.

The broker checks Deployment identity/version and controller observations again
before sending one guarded patch. A stale review must be refreshed and approved
again. Approval and execution intent must reach both the durable journal and
logbook before sending. The broker continues readiness monitoring after the TUI
closes; reopen Incoming proposals and select the operation to see its outcome.
Controller completion does not establish application recovery.

Deployment scaling, a named regular container's image, restart with a frozen
reviewed timestamp, and rollback to an exact owned ReplicaSet revision are
supported. Image/restart patches preserve other fields; rollback replaces the
reviewed pod template. Diffs mask literal environment values and apply configured
redaction; executable template bytes stay in broker memory. An interrupted or
ambiguous write is never automatically replayed.

Run `safina mcp` from an MCP client to use the official Go SDK stdio adapter.
Stdout contains protocol messages only. The nine tools are `list_contexts`,
`start_investigation`, `inspect_workload`, `read_events`, `read_logs`,
`query_history`, `get_evidence`, `submit_proposals`, and `get_investigation`.
No tool approves, rejects, overrides or executes a Kubernetes mutation.
Investigation creation, proposals and evidence capture persist local data and
are not advertised as universally read-only.

Start requires an explicit context and returns the last 24 hours of relevant
retained activity (at most 50 records / 16 KiB). A supplied workload target
prioritizes that resource and related workload records; without a target the
brief covers the pinned cluster. `query_history` supports deeper time windows
and resource filters with at most 100 records / 16 KiB per response. Operator
reviews show recent attempts of the same action kind, including failed and
uncertain outcomes. Missing history does not prove inactivity.

Inspection selects fields and excludes environment/Secret values. Logs are
bounded to 200 lines / 16 KiB; events and related objects are limited to 100 per
list. Responses identify scope, observation time, truncation and permission or
coverage failures. Evidence is untrusted data, never instructions. Captured
responses are redacted before return/storage and retained with the investigation
(up to 64 captures of 16 KiB each, independently of logbook retention). Disable
new evidence persistence with `history.evidence: false`; live reads then return
without a stored evidence ID. Proposal evidence IDs must belong to that
investigation; fetching scoped historical evidence returns a new captured ID.
Historical Secret contents are excluded, and expired evidence stays explicit.

Existing manual workflows remain available. After upgrading an already-running
broker, explicitly stop it with `safina broker stop`; the next client starts the
new binary. Load the [Claude plugin](../plugins/safina/README.md) to use
`/safina:investigate`. Release validation progress and limitations are recorded
in the [workflow plan](plans/claude-safina-workflow.md).

```sh
safina serve                 # foreground broker (normally started by a client)
safina broker status         # readiness, pending logbook delivery, recording gaps
safina broker stop           # explicit shutdown; keeps investigations on disk
```

The default owner-only Unix socket is `~/.safina/run/broker.sock`; durable
investigations live in `~/.safina/investigations/journal.jsonl`, independently of
logbook retention. `serve` accepts `--runtime-dir`, `--state-dir`, `--kubeconfig`
and `--history-dir`; status/stop accept `--runtime-dir`. These commands do not
choose the current kubeconfig context implicitly. Creating an investigation
requires an explicit context; proposals remain unverified until broker review.

Clients keep independent session IDs and reuse the same idempotency key when
retrying a submission with an uncertain response. Restart restores pending work;
recovered execution intents become unknown and are never replayed. Logbook
recording failures retain a durable delivery queue and appear in broker status.
The socket and peer checks are local access controls, not a sandbox against
arbitrary code running as the same user.
