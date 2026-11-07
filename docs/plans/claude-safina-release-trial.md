# Claude/Safina release trial

Use a disposable cluster only. The plugin setup is in
[plugins/safina/README.md](../../plugins/safina/README.md). Run Claude and Safina
as separate terminal processes. Do not use a production kubeconfig for this test.

## Reproduce

Build `make build`. Choose a new temporary directory and unused minikube profile:

```sh
trial_dir=$(mktemp -d /tmp/safina-trial.XXXXXX)
trial_profile="safina-trial-$(date +%s)"
KUBECONFIG="$trial_dir/kubeconfig" minikube start -p "$trial_profile" --driver=docker
kubectl --kubeconfig "$trial_dir/kubeconfig" create namespace trial
kubectl --kubeconfig "$trial_dir/kubeconfig" -n trial create deployment web --image=nginx:1.27-alpine --replicas=2
kubectl --kubeconfig "$trial_dir/kubeconfig" -n trial rollout status deployment/web --timeout=120s
cp -R plugins/safina "$trial_dir/plugin"
```

If the node cannot pull the image, pull it on the host and run
`minikube -p "$trial_profile" image load nginx:1.27-alpine` with the trial
KUBECONFIG. Resolve setup readiness before calling the test an incident workflow.

In the copied plugin's `.mcp.json`, set `command` to the absolute built binary
and `args` to `mcp` followed by these absolute path arguments:

```text
--kubeconfig TRIAL/kubeconfig
--runtime-dir TRIAL/run
--state-dir TRIAL/state
--history-dir TRIAL/history
```

Start Claude with `claude --plugin-dir "$trial_dir/plugin"`. In the second
terminal run the same binary and path arguments (without the `mcp` subcommand),
plus `--context "$trial_profile" --namespace trial`. These flags isolate the
broker, journal and logbook; they do not relocate Safina's general app settings,
preferences or diagnostics. Do not repurpose HOME or change the normal kubeconfig.

1. Establish prior logbook context: perform and observe a manual Safina test
   change first, or seed a clearly labeled fixture record into the isolated
   logbook. A synthetic record is test data, not evidence of a real prior repair.
2. Invoke `/safina:investigate CONTEXT trial/web` and explain that this is an
   acceptance test. Request scale to 3 and restart as alternative test operations.
   Confirm Claude reads the prior record and current captured evidence before
   submission. Record investigation/group/evidence IDs.
3. In Safina open `:` → Incoming proposals, select scale, inspect evidence and
   summary, edit replicas to 4, and inspect the regenerated full diff. Approve
   with `a`, then `y`. Confirm the selected revision differs from Claude's original
   and the restart sibling is unavailable. Exit the TUI; leave the broker running.
4. Ask the same Claude session to retrieve that investigation. Verify it reports
   the selected revision, broker outcome and historical links. Check fresh
   evidence and confirm it distinguishes controller readiness from application
   recovery. Reopen Safina and inspect the recorded outcome.
5. All four actions must pass the repository's MCP/TUI integration tests. For
   additional live coverage, repeat with image, restart and a pinned observed
   ReplicaSet revision; pre-load both image versions and never resolve "previous"
   during approval. Do not claim these live cases ran merely because fixture
   integration tests pass.

Stop the trial broker and delete only the profile created for this trial:

```sh
bin/safina broker stop --runtime-dir "$trial_dir/run"
KUBECONFIG="$trial_dir/kubeconfig" minikube delete -p "$trial_profile"
```

Keep local trial artifacts only as long as needed. Review redacted evidence and
Claude session data before sharing. Deleting the cluster does not delete the
journal, logbook or Claude session.

## Results — 2026-11-07

Results and limitations are recorded in the M4 session entry in
[the implementation plan](claude-safina-workflow.md).

| Check | Observed result |
| --- | --- |
| Runtime | Claude Code 2.1.319, minikube 1.38.1, Kubernetes 1.37.2, Docker driver |
| Isolation | Profile `safina-m4-20261107`; explicit kubeconfig, runtime, state and history under `/tmp/safina-m4-trial` |
| Prior context | Labeled setup fixture record `9712133575e5482e98a2eeab83533672`; Claude explicitly distinguished it from a prior repair |
| Investigation | `38e42c5f8740187faf60dd077032fb36` |
| Alternatives | Group `816bb2cd8b112241ae95b4637f262ee7`: scale to 3 and restart, both labeled test operations |
| Operator path | Separate 80×24 Safina PTY, driven by test keystrokes; scale edited to 4, revision 2, regenerated diff visited, `a` then `y` |
| Selected proposal | `f073d239d9b4530cc39ae75fabb5ce13`, revision 2; revision 1 superseded, restart sibling unavailable |
| Operation | `96bb7f9541da2120dd8e0a54b087cf76`, completed; Deployment generation 3, 4/4 available |
| Follow-up | Same real Claude session read outcome, fresh workload/events/logs and linked history; zero pending deliveries; application recovery explicitly unverified |
| Reopen | New Safina PTY opened revision 2 and displayed completed controller outcome |

Claude ran in print/resume mode with only the Skill built-in and the Safina MCP
tools allowed, without permission bypass. Slash-command expansion loaded
`/safina:investigate`; tool traces show no calls to other servers. The operator
role was exercised by automated PTY keystrokes, not a human usability study.
The real cluster trial executed scale; all four action kinds additionally passed
the automated HTTP-backed MCP/TUI acceptance tests. Live image/restart/rollback,
macOS runtime behavior and arbitrary admission/controller configurations were not
validated in this trial. Native and macOS ARM64 builds passed.

Minikube warned that Kubernetes 1.37.2 was newer than its supported version list,
but downloaded it and completed cluster initialization. The initial pod image
pull failed; loading the host's `nginx:1.27-alpine` image and updating the fixture
made the workload ready before proposals. The first Claude smoke call preceded
kubeconfig creation and failed; subsequent real calls succeeded. These setup
failures are not counted as successful application recovery tests.

Full local artifacts (not committed): `cluster.log`, `claude-smoke.jsonl`,
`claude-investigate.jsonl`, `claude-outcome.jsonl`, the isolated journal/logbook,
and `cleanup.log` in `/tmp/safina-m4-trial`. These include Claude session output;
review before sharing. The disposable broker was explicitly stopped and its
minikube profile deleted. No production context was contacted.
