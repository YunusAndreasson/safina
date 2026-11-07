import type { ImageMetadata } from "astro";
import workloads from "../assets/shots/workloads.png";
import workloadsM from "../assets/shots/workloads-m.png";
import failing from "../assets/shots/failing.png";
import failingM from "../assets/shots/failing-m.png";
import logs from "../assets/shots/logs.png";
import logsM from "../assets/shots/logs-m.png";
import events from "../assets/shots/events.png";
import eventsM from "../assets/shots/events-m.png";
import hot from "../assets/shots/hot.png";
import hotM from "../assets/shots/hot-m.png";

// The tour: real screens from a real session against the demo cluster
// (site/scripts), one per key. All of these keys only read the cluster.
export interface Stop {
  id: string;
  /** KeyboardEvent.key that selects this stop */
  key: string;
  /** what the keycap shows */
  glyph: string;
  name: string;
  /** Plain text; `_word_` marks an Arabic term (lib/typeset `runs`). */
  caption: string;
  alt: string;
  shot: ImageMetadata;
  crop: ImageMetadata;
}

export const tour = [
  {
    id: "drill",
    key: "Enter",
    glyph: "↵",
    name: "Drill in",
    caption:
      "Clusters, namespaces, workloads, pods: Enter goes deeper, Esc goes back, the inspector follows the cursor.",
    alt: "safina showing every workload in the cluster with its readiness and CPU and memory trend lines; ledger-api and search-indexer show none ready, in red. payments-api is selected; the inspector shows its live CPU graph, replicas, and the config map and secret it uses.",
    shot: workloads,
    crop: workloadsM,
  },
  {
    id: "next",
    key: "n",
    glyph: "n",
    name: "Next failing",
    caption: "Straight to the next crash loop, unschedulable pod or stalled rollout. No scanning for red.",
    alt: "The cursor has jumped to ledger-api, 0 of 2 ready. The inspector reads Unavailable: 2 updated, 0 ready, 2 desired.",
    shot: failing,
    crop: failingM,
  },
  {
    id: "logs",
    key: "l",
    glyph: "l",
    name: "Logs",
    caption: "The pod's logs, live or from the last crash, with levels, JSON and stack traces picked out.",
    alt: "The last crashed container's logs for a ledger-api pod: three info lines, three warnings that the database is not reachable, an error from a failed DNS lookup of ledger-db-primary, then a Go panic with its stack trace.",
    shot: logs,
    crop: logsM,
  },
  {
    id: "events",
    key: "e",
    glyph: "e",
    name: "Events",
    caption: "Why Kubernetes did what it did: events for the selected resource, newest first, warnings lit.",
    alt: "Events for a ledger-api pod, newest first: a Back-off restarting failed container warning seen 205 times, the image pulled again 49 times, and the container created 44 times.",
    shot: events,
    crop: eventsM,
  },
  {
    id: "hot",
    key: "h",
    glyph: "h",
    name: "Hot pods",
    caption: "Every pod in the cluster, weighed by CPU, from wherever you are.",
    alt: "The hot pods table: every pod in the cluster ranked by CPU, led by settlement-worker at 529m and two payments-api pods, with namespace, status, memory and restarts.",
    shot: hot,
    crop: hotM,
  },
] satisfies [Stop, ...Stop[]];
