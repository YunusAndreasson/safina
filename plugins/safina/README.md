# Safina for Claude Code

Investigate a Deployment in your regular Claude Code session, then review and
approve a typed change in a separate Safina terminal. Requires a built Safina
binary, Claude Code with plugin support, and a kubeconfig whose RBAC permits the
reads and the changes you intend to approve. No marketplace installation or
account configuration is changed by this local setup.

From the repository root:

```sh
make build
export PATH="$PWD/bin:$PATH"
export KUBECONFIG=/absolute/path/to/your/kubeconfig
claude --plugin-dir "$PWD/plugins/safina"
```

Accept Claude's MCP connection prompt if shown. Use `/mcp` to check the Safina
server, then invoke, for example:

```text
/safina:investigate my-context payments/api Investigate unavailable replicas
```

In another terminal with the same binary and kubeconfig:

```sh
safina --kubeconfig /absolute/path/to/your/kubeconfig --context my-context
```

Use `:` → **Incoming proposals**. Enter opens a proposal, `Tab` cycles summary,
diff and evidence, and `d` opens the complete scrollable diff. `e` edits replicas,
`container=image`, or an exact rollback revision; restart has no parameters.
`x` rejects. Review any HPA/GitOps warning and explicitly acknowledge an override
with `o` if appropriate. After visiting the diff, `A` then `y` approves the reviewed
revision. Changing live state requires refreshed review. Tell Claude to retrieve
the investigation after your review; pending is not approval.

The MCP server starts the owner-only broker on demand. Closing Claude or Safina
does not stop it; approved readiness monitoring continues. These commands do not
auto-start a broker:

```sh
safina broker status
safina broker stop
```

Stop an old broker explicitly after upgrading, then let the next client start the
new binary. Pending work survives restart; uncertain writes are never replayed.

## Configuration and data

The plugin's `.mcp.json` runs `safina mcp` from PATH and inherits KUBECONFIG.
Use an absolute executable path in a local copy if PATH differs in Claude.
For isolated trials, append the same `--kubeconfig`, `--runtime-dir`,
`--state-dir`, and `--history-dir` arguments to both the MCP configuration and
the Safina TUI command. Status/stop need the matching `--runtime-dir`.
Do not run two brokers against the same state directory. An already-running
broker owns its history/redaction configuration; stop it before changing that
configuration. App settings remain in `~/.safina/config.yaml`.

Default storage:

| Data | Location |
| --- | --- |
| Socket and diagnostics | `~/.safina/run/broker.sock`, `broker.log` |
| Durable investigations, proposals, decisions, outcomes and captures | `~/.safina/investigations/journal.jsonl` |
| Logbook and historical evidence | `~/.safina/history/` |

Evidence returned by MCP enters the Claude session and is disclosed to its
configured model provider. Safina does not save the full conversation, but Claude
has its own session retention. Redaction reduces accidental disclosure; it cannot
recognize every sensitive application log. Configure `history.redactPatterns`
in Safina's configuration for your data before investigating.

Inspection excludes literal environment and Secret values. Logs are bounded to
200 lines / 16 KiB; events and related lists to 100 objects. Evidence captures are
redacted before return and storage, at most 16 KiB each and 64 per investigation.
`history.evidence: false` disables new capture persistence, not redacted live
responses to Claude. Investigation data has no automatic pruning/compaction and
is independent of logbook retention. `history.enabled: false` disables new
logbook recording and blocks proposed execution that requires durable delivery;
it does not erase investigations or disable their persistence. Missing evidence,
permission errors and truncated responses remain explicit.

The start brief covers 24 hours, up to 50 records / 16 KiB; deeper history queries
return up to 100 records / 16 KiB. History is local activity, not cluster-wide
or tamper-proof auditing. Current redaction is reapplied to returned older data;
it does not rewrite old files on disk.

## Capability boundary

The nine MCP tools inspect, capture evidence, query history, create investigations,
submit proposals and retrieve outcomes. They cannot approve or execute a change.
Supported proposals are Deployment scale, a named regular container's image,
restart with a frozen reviewed annotation, and rollback to a pinned owned
ReplicaSet revision. No shell, pod exec, arbitrary YAML, Secret-value retrieval,
automatic remediation or automatic rollback is exposed. Local submissions and
captures write data; they are not universally read-only operations.

The operator approves in Safina. The broker rechecks identity/version, durably
records intent, sends one guarded patch and monitors controller readiness.
Controller completion does not prove application recovery. HPA/GitOps may reverse
an action, and admission controllers may modify or reject it.

The plugin does not change Claude permissions or grant shell access. Other Claude
tools and credentials are outside this boundary; same-user code can access local
files and sockets. Owner-only storage is not a sandbox. Do not use bypass-permission
mode to make the plugin work.

## Validation

Run `claude plugin validate --strict plugins/safina` from the repository root and
`make verify-all`. See [the release trial](../../docs/plans/claude-safina-release-trial.md)
for isolated cluster setup and acceptance evidence. Real Claude invocation and
operator review must be tested in addition to the automated HTTP fixtures.

Packaging follows the official [plugin manifest](https://code.claude.com/docs/en/plugins-reference)
and [skills](https://code.claude.com/docs/en/skills) documentation. This plugin has
no hooks, automatic permission grants, or bundled executable; it uses the Safina
binary you install.
