# The world the copy lands in

What a reader who runs Kubernetes on call already knows, and where the site
leans on it. Defaults change between versions. Before printing a number,
check it against the current Kubernetes docs (context7 or kubernetes.io) and
against safina's code.

## Kubernetes behaviour the copy relies on

- **Events expire.** kube-apiserver keeps events for `--event-ttl`, 1h by
  default, and most managed clusters keep that. "Kubernetes keeps events for
  an hour" is true by default, not by law.
- **Logs leave with the pod.** The kubelet keeps the running container's log
  and the previous instance's (`kubectl logs --previous`). Once a pod is
  deleted or replaced (a rollout, an eviction, a lost node), its logs are gone
  unless something shipped them.
- **Crash loops back off.** Restarts wait 10s, then double up to a 5-minute
  cap, and the back-off resets after 10 minutes of clean running. The event
  reads "Back-off restarting failed container" (it's in the events shot).
  Newer releases can tune this behind feature gates.
- **Exit codes.** 137 is SIGKILL, often `OOMKilled` in `lastState`; 143 is
  SIGTERM.
- **Unschedulable** means Pending with a `FailedScheduling` event ("0/N nodes
  are available: …").
- **Accepted isn't done.** A scale or restart returns as soon as the API
  stores it. Pods are ready later, or never. A Deployment reports
  `ProgressDeadlineExceeded` after `progressDeadlineSeconds`, 600s by default;
  `kubectl rollout status` waits on that. Safina's Rollout watch gives up and
  tells you after 5 minutes (`internal/tui/operations.go`).
- **Restart and rollback.** `kubectl rollout restart` stamps the pod template
  with a `restartedAt` annotation, which starts a new rollout. `rollout undo`
  returns to the previous ReplicaSet (10 kept by default).
- **Debug containers stay.** An ephemeral container (`kubectl debug`) can't be
  removed from a pod once added; it goes when the pod goes. That's why safina
  asks before adding one.
- **Port-forwards are fragile.** A forward to a Service picks one pod and dies
  with it, and it lives in whichever terminal tab started it.
- **CPU and memory come from metrics-server.** No metrics-server, no numbers
  (`kubectl top` fails the same way). minikube doesn't enable it by default.
- **Secrets are base64, not encrypted**, as far as the API is concerned.
  Anyone who can `get secrets` reads them. That's what gives "never a
  Secret's data" its weight.

## Access, contexts, auth

- A kubeconfig context is a cluster, a user and an optional namespace.
  `kubectl config use-context` writes `current-context` into the file, so
  every shell reading that file switches with it. Safina loads the kubeconfig
  with kubectl's own rules (`--kubeconfig`, then `$KUBECONFIG` merged, then
  `~/.kube/config`; `internal/config/kubeconfig.go`), keeps a client per
  context, and never writes the file.
- Most clusters authenticate through exec plugins (`aws eks get-token`,
  `gke-gcloud-auth-plugin`, `kubelogin`), some of which open a browser.
  "Auth plugins included" is the line that answers that worry.
- RBAC is the ceiling: a tool on your kubeconfig can do exactly what your user
  can (`kubectl auth can-i`). "Safina can only do what your RBAC lets you do"
  is the honest form of a safety claim.

## The night itself

- The page arrives on a phone; the work happens on a laptop, often in a shell
  on a bastion. Copy over SSH exists because the clipboard is on the other
  side of that connection (safina writes OSC 52 when no clipboard tool is
  there; `internal/tui/update.go`).
- The first questions are always the same: which cluster am I on, what's
  broken, since when, what changed. The site's order (No autopilot, `n`,
  logs, events, the Logbook) follows them.
- The postmortem comes days later and needs a timeline. By then the events
  have expired and the pods have been replaced, which is the Logbook's whole
  case. It keeps writes for 90 days and what you read for 14
  (`internal/history/store.go`).

## Words the field uses

on call (on-call engineer, being paged), incident, postmortem, runbook, crash
loop or CrashLoopBackOff, rollout, rollback, scale, cordon, drain, evict,
context, namespace, workload. Write "the cluster", not "your infrastructure";
"pods" or "workloads", not "services" unless you mean the Service object.
