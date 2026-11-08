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
import metrics from "../assets/shots/metrics.png";
import metricsM from "../assets/shots/metrics-m.png";
import workloadsCast from "../assets/casts/workloads.cast?url";
import failingCast from "../assets/casts/failing.cast?url";
import logsCast from "../assets/casts/logs.cast?url";
import eventsCast from "../assets/casts/events.cast?url";
import hotCast from "../assets/casts/hot.cast?url";
import metricsCast from "../assets/casts/metrics.cast?url";

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
  /** The still's name in crops.json (its crop boxes). */
  still: "workloads" | "failing" | "logs" | "events" | "hot" | "metrics";
  /** The key's moment, recorded (asciicast): it plays over the screen and
   *  ends on this stop's still. */
  cast?: string;
}

export const tour = [
  {
    id: "drill",
    key: "Enter",
    glyph: "↵",
    name: "Drill in",
    caption:
      "Enter goes from cluster to namespace to workload to pod, and Esc comes back up.",
    alt: "safina listing every workload in the cluster with its kind, readiness and CPU and memory trend lines; ledger-api and search-indexer show none ready, in red. The cursor rests on the ledger-db StatefulSet, two rows below ledger-api and healthy at 1 of 1.",
    shot: workloads,
    still: "workloads",
    cast: workloadsCast,
    crop: workloadsM,
  },
  {
    id: "next",
    key: "n",
    glyph: "n",
    name: "Next failing",
    caption: "Straight to the next crash loop, unschedulable pod or stalled rollout.",
    alt: "The cursor has jumped to ledger-api. The inspector reads Unavailable: 2 updated, 0 ready, 2 desired, above its strategy, selector, image and CPU request and limit.",
    shot: failing,
    still: "failing",
    cast: failingCast,
    crop: failingM,
  },
  {
    id: "logs",
    key: "l",
    glyph: "l",
    name: "Logs",
    caption: "The pod’s logs, live or from the last crash.",
    alt: "Inside ledger-api: its two pods at 7 restarts each, and one of them crashing live. It starts at 11:15:50, fails to reach the database three times, fails a DNS lookup of ledger-db-primary, and panics four seconds in, with a Go stack trace.",
    shot: logs,
    still: "logs",
    cast: logsCast,
    crop: logsM,
  },
  {
    id: "events",
    key: "e",
    glyph: "e",
    name: "Events",
    caption: "Events for whatever you’ve selected. Usually this is where Kubernetes explains itself.",
    alt: "Events for that pod, newest first: a Back-off restarting failed container warning seen 19 times, its image pulled and the container created and started 9 times each, and the pod scheduled 16 minutes ago.",
    shot: events,
    still: "events",
    cast: eventsCast,
    crop: eventsM,
  },
  {
    id: "hot",
    key: "h",
    glyph: "h",
    name: "Hot pods",
    caption: "Every pod in the cluster weighed by CPU, from whichever screen you’re on.",
    alt: "The hot pods table: every pod in the cluster ranked by CPU, led by settlement-worker at 734m and two payments-api pods, with namespace, status, memory and restarts.",
    shot: hot,
    still: "hot",
    cast: hotCast,
    crop: hotM,
  },
  {
    id: "metrics",
    key: "m",
    glyph: "m",
    name: "Metrics",
    caption:
      "CPU and memory over time for whatever you’ve selected, from five minutes up to six hours.",
    alt: "settlement-worker’s metrics over the last 15 minutes: CPU at 366m with a peak of 767m, swinging between about 300m and 770m, a gap where safina wasn’t sampling it, and memory flat at 2Mi, peaking at 3Mi.",
    shot: metrics,
    still: "metrics",
    cast: metricsCast,
    crop: metricsM,
  },
] satisfies [Stop, ...Stop[]];
