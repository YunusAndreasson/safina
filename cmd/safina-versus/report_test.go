package main

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestReportVerdictsAndGaps(t *testing.T) {
	res := &Results{Env: Environment{Seed: 1, Versions: map[string]string{"safina": "dev", "k9s": "v0"}, Fixture: map[string]int{"pods": 3}}}
	for i := range 30 {
		jitter := float64(i%5) / 10
		res.Samples = append(res.Samples,
			Sample{Tool: "safina", Op: opCursor, LatencyMs: 10 + jitter},
			Sample{Tool: "k9s", Op: opCursor, LatencyMs: 20 + jitter},
			Sample{Tool: "safina", Op: opYAML, LatencyMs: 15 + jitter},
			Sample{Tool: "k9s", Op: opYAML, LatencyMs: 15 + jitter},
			Sample{Tool: "k9s", Op: opLogs, LatencyMs: 50},
		)
	}
	res.Sessions = []SessionStat{
		{Tool: "safina", RSS: 100 << 20, IdleSeconds: 30, IdleCPUMs: 150, IdleEndpoints: map[string]int64{"LIST /api/v1/pods": 60}},
		{Tool: "safina", RSS: 100 << 20, IdleSeconds: 30, IdleCPUMs: 150, IdleEndpoints: map[string]int64{"LIST /api/v1/pods": 90}},
		{Tool: "k9s", RSS: 200 << 20, IdleSeconds: 30, IdleCPUMs: 1500, IdleEndpoints: map[string]int64{"WATCH /api/v1/pods": 1}},
		{Tool: "k9s", Warmup: true, RSS: 1},
		{Tool: "safina", Error: "boom"},
	}
	got := map[string]string{}
	for _, v := range Compare(res) {
		got[v.Op] = v.Result
	}
	want := map[string]string{opCursor: "safina faster", opYAML: "no difference", opLogs: "no data"}
	for op, w := range want {
		if got[op] != w {
			t.Errorf("%s: %q, want %q", op, got[op], w)
		}
	}
	var b bytes.Buffer
	WriteMarkdown(&b, res)
	out := b.String()
	for _, s := range []string{
		"| Cursor down one row | 30/30 | 10.", "| Open logs, first line shown | 0/30 | — |", "Failed launches", "boom", "| Idle CPU | 0.5 % of a core | 5.0 % of a core | 0.10 [0.10–0.10] | **safina lower** |",
		"| `LIST /api/v1/pods` | 150.0 | 0.0 |", "| `WATCH /api/v1/pods` | 0.0 | 2.0 |",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("report lacks %q:\n%s", s, out)
		}
	}
}

func TestEndpointKeyDropsNames(t *testing.T) {
	for raw, want := range map[string]string{
		"/api/v1/pods?limit=500":                                       "LIST /api/v1/pods",
		"/api/v1/namespaces/bench-3/pods/app-1-abc":                    "GET /api/v1/namespaces/{ns}/pods/{name}",
		"/api/v1/namespaces/bench-3/pods/app-1-abc/log?follow=true":    "GET /api/v1/namespaces/{ns}/pods/{name}/log",
		"/api/v1/namespaces/bench-3/events?fieldSelector=x":            "LIST /api/v1/namespaces/{ns}/events ?fieldSelector",
		"/apis/apps/v1/deployments?watch=true&resourceVersion=1":       "WATCH /apis/apps/v1/deployments",
		"/apis/apps/v1/namespaces/bench-0/replicasets?labelSelector=a": "LIST /apis/apps/v1/namespaces/{ns}/replicasets ?labelSelector",
		"/api/v1/namespaces/bench-0":                                   "GET /api/v1/namespaces/{name}",
		"/version":                                                     "GET /version",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		r := &http.Request{Method: http.MethodGet, URL: u}
		if got := endpointKey(endpointVerb(r), r); got != want {
			t.Errorf("%s: %q, want %q", raw, got, want)
		}
	}
}
