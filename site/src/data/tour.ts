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
      "Clusters, namespaces, workloads, pods. Enter goes one level deeper, Esc goes back, and the inspector on the right follows the cursor.",
    alt: "safina showing every workload in the cluster with CPU and memory trend lines. payments-api is selected; the inspector shows its live CPU graph, replicas, and the config map and secret it uses.",
    shot: workloads,
    crop: workloadsM,
  },
  {
    id: "next",
    key: "n",
    glyph: "n",
    name: "Next failing",
    caption: "Jumps to the next resource with a problem: a crash loop, a pod that cannot be scheduled, a stalled rollout.",
    alt: "The cursor has jumped to ledger-api, marked as failing. The inspector reads Unavailable, with 0 of 2 replicas ready.",
    shot: failing,
    crop: failingM,
  },
  {
    id: "logs",
    key: "l",
    glyph: "l",
    name: "Logs",
    caption: "Streams the pod's logs live, with log levels, JSON and stack traces highlighted.",
    alt: "Live logs for a ledger-api pod: info lines, three warnings that the database is unreachable, an error from a failed DNS lookup, then a Go panic with its stack trace.",
    shot: logs,
    crop: logsM,
  },
  {
    id: "events",
    key: "e",
    glyph: "e",
    name: "Events",
    caption: "Kubernetes events for the selected resource, newest first, with warnings highlighted.",
    alt: "Events for the ledger-api pod, newest first: a Back-off restarting failed container warning, then the container created, pulled and started again; the same cycle two hours earlier; and the pod first being scheduled.",
    shot: events,
    crop: eventsM,
  },
  {
    id: "hot",
    key: "h",
    glyph: "h",
    name: "Hot pods",
    caption: "The pods using the most CPU across the whole cluster, from wherever you are.",
    alt: "The hot pods table: every pod in the cluster ranked by CPU, led by settlement-worker and payments-api, with namespace, status, memory and restarts.",
    shot: hot,
    crop: hotM,
  },
] satisfies [Stop, ...Stop[]];
