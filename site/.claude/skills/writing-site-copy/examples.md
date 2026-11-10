# Before and after

## From this site's own revisions

These were real drafts. The user rejected the "before" each time.

**A gracious concession, then a slogan**

> Before: k9s does more and has years of hard use behind it. If it works for
> you, keep it. Safina does less, and what it does is aimed at the bad night: …
>
> After: a table of checks and dashes (`Compare.astro`), and above it: "Both
> read the same kubeconfig, so you can run them side by side."

The concession flatters to look fair, and "aimed at the bad night" is a
slogan standing where a fact belongs. The replacement is a fact a k9s user
can act on today.

**A punch fragment**

> Before: …the keys that write are red. No scanning for red.
>
> After: the fragment is gone.

The sentence before it had already said it. The fragment is there for
rhythm, and readers hear that.

**A closing aphorism**

> Before: The right command on the wrong cluster is how a lot of bad nights
> start.
>
> After: No autopilot means safina never picks a cluster for you, not even the
> one you used last time, so nothing runs against yesterday’s cluster by
> default.

A proverb instead of a mechanism. The rewrite says what safina does and what
follows from it.

**Mirrored negation**

> Before: Nothing to paste, nothing to explain twice.
>
> After: …so you don’t paste stack traces into a browser tab.

"Nothing X, nothing Y" is a rhythm, not a fact. The rewrite names the
actual chore it removes.

**Stiff, uncontracted**

> Before: You have selected a workload.
>
> After: what you’ve selected

**A reason worth keeping**

> That works without cluster access, which helps when the cluster was the
> problem.

A blind read with this sentence cut made the Logbook section less clear, not
more. Cut description, never the reason a feature exists.

**The tagline**

> The tagline (since Nov 2026 "The Kubernetes TUI for teams that ship fast.") was
> once swapped for "for bad nights" as a supposed stock phrase. It was
> restored. Never touch it; only the user changes it.

## Illustrations in this domain

These show the move; they were never on the site.

**Category word → the act**

> Before: Gain instant visibility into your cluster’s health.
>
> After: Press n and the cursor jumps to the next crash loop, unschedulable
> pod or stalled rollout.

"Visibility" and "health" describe a feeling. The rewrite describes a
keypress and names the three failure states a reader recognises.

**Hedge → mechanism**

> Before: Safina’s powerful AI integration helps you troubleshoot faster.
>
> After: Press i and ask what’s wrong. Claude gets the cluster, namespace and
> resource you’re on and what you’ve already tried there.

"Helps you" and "powerful" claim without committing. The rewrite says what
Claude receives, which is the claim an engineer actually checks.

**Overclaim → the true, smaller claim**

> Before: Designed with safety in mind, safina ensures you never make a
> mistake.
>
> After: Every key that changes the cluster is a capital letter, so a slipped
> finger can only ever read.

The first is false (you can still scale the wrong thing on purpose). The
second is exactly as strong as the code.

**Feature list → a reason only a practitioner knows**

> Before: Switch clusters in the middle of an incident without quitting
> safina.
>
> Deeper: safina keeps a client per context and never writes your
> kubeconfig (`internal/k8s/client.go`), so switching inside it doesn't
> change the context kubectl uses in your other terminals. `kubectl config
> use-context` does.

The first is true but anyone could have written it. The second needs the
code and the habit of being bitten by `use-context` at 3am. Depth like that
is what makes a line read as written by someone who has been there.

**Alt text: what the screen says, not that it exists**

> Before: Screenshot of the safina dashboard.
>
> After: The cursor has jumped to ledger-api, 0 of 2 ready. The inspector
> reads Unavailable: 2 updated, 0 ready, 2 desired.

Alt text is the only version of the screenshot some readers get, so unlike
body copy it does describe the screen, with the same names and numbers.

**A claim that was true for one path only**

> Before: In the Logbook, `c` hands it the logs you’ve been reading.
>
> After: Open a logs entry in the Logbook and `c` hands it the lines you read.

On the Logbook's list, `c` attaches only the records' metadata; from an
opened entry it attaches the captured lines (`internal/tui/feedback.go`).
The re-shoot found it: the model answered "The attachment had no log or
event contents." Drive the feature before you describe it, and say which
path does what.

**A vendor name where a role will do**

> Before: Ask Claude. Claude gets the cluster, namespace and resource you’re on.
>
> After: Chat. The model gets the cluster, namespace and resource you’re on.

The app calls the panel Chat, and other models may follow. Name the vendor
only for a fact the reader needs today, such as the `claude` CLI that chat
runs through.
