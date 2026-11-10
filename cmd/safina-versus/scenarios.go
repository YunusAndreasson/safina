package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Script is one tool's way of performing the shared operations. Every op
// starts and ends on the tool's all-namespace pod list with no filter.
type Script struct {
	Launch func(r *Rec, start time.Time) error
	Ops    map[string]func(*Rec) error
}

const (
	opFirstData = "first-data"
	opStartup   = "startup"
	opCursor    = "cursor"
	opFilterKey = "filter-key"
	opFilter    = "filter"
	opYAML      = "yaml"
	opYAMLClose = "yaml-close"
	opSwitch    = "switch"
	opSwitchBk  = "switch-back"
	opLogs      = "logs"
)

// opCatalog fixes the report order. first-data and startup are recorded by
// Launch; the rest run in this order within each launch.
var opCatalog = []struct{ ID, Title string }{
	{opFirstData, "Launch to first cluster data"},
	{opStartup, "Launch to all-namespace pod list"},
	{opCursor, "Cursor down one row"},
	{opFilterKey, "Keystroke while filtering"},
	{opFilter, "Filter pods by name (typed at once)"},
	{opYAML, "Open YAML of a pod"},
	{opYAMLClose, "Close YAML"},
	{opSwitch, "Pods → workloads list"},
	{opSwitchBk, "Workloads → pods list"},
	{opLogs, "Open logs, first line shown"},
}

func opTitle(id string) (string, bool) {
	for _, o := range opCatalog {
		if o.ID == id {
			return o.Title, true
		}
	}
	return "", false
}

// The fixture names: deployments app-N in namespaces bench-N, plus
// bench-logs/chatter, which prints "chatter tick N" every second.
const (
	filterTerm    = "app-12"
	filterMatches = 10 // app-12 runs once in each of bench-0 … bench-9
	cursorPresses = 20
	logLine       = "chatter tick"
)

func Regexp(desc, expr string) Predicate {
	re := regexp.MustCompile(expr)
	return Predicate{Desc: desc, Match: func(s *Screen) bool { return re.MatchString(s.Plain()) }}
}

// TopRows matches when the first n list rows (lines matching row, whose
// first group is the name) are all named want. safina's filter is fuzzy and
// ranks exact matches first, k9s's keeps only exact matches; both put the n
// wanted rows on top.
func TopRows(row, want string, n int) Predicate {
	re := regexp.MustCompile(row)
	return Predicate{Desc: fmt.Sprintf("top %d rows are %s", n, want), Match: func(s *Screen) bool {
		seen := 0
		for _, line := range strings.Split(s.Plain(), "\n") {
			m := re.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			if m[1] != want {
				return false
			}
			if seen++; seen == n {
				return true
			}
		}
		return false
	}}
}

// startup records both launch ops: first cluster data, then the pod list.
func recordLaunch(r *Rec, start time.Time, first Predicate, toPods func() (time.Time, error)) error {
	st, err := r.s.Expect(first, start, r.timeout)
	if err != nil {
		return r.fail(opFirstData, err)
	}
	r.add(opFirstData, st)
	done, err := toPods()
	if err != nil {
		return r.fail(opStartup, err)
	}
	r.s.Quiet()
	r.add(opStartup, Step{Latency: done.Sub(start)})
	return nil
}

// --- safina -----------------------------------------------------------------

var (
	sfNamespaces = All(Contains("╭─ Browse cluster"), Regexp("namespace rows", `(?m)^│ bench-0\s`))
	sfTypes      = Contains("╭─ Resource types")
	// A pod row; the namespace column is left out of the pattern because
	// safina hides it when every row shown is in one namespace.
	sfPodRowRe = `^│ ([a-z]+(?:-\d+)?)-[0-9a-z]+-[0-9a-z]{5}\s`
	// Panel titles may carry the refresh spinner: "╭─ Pods ⠏ ─".
	sfPods = All(Regexp("pods panel", `╭─ Pods (\S+ )?─`), Regexp("pod rows", `(?m)^│ app-0-`))
	// Back on the list after a filter or view change: safina keeps the
	// selected pod in view, so any rows will do once no filter is active.
	sfPodList = All(Regexp("pods panel", `╭─ Pods (\S+ )?─`), Regexp("pod rows", `(?m)^│ app-\d+-`), Not(Contains("│/")),
		Not(TopRows(sfPodRowRe, filterTerm, filterMatches)))
	sfWorkloads = All(Regexp("workloads panel", `╭─ Workloads (\S+ )?─`), Regexp("workload rows", `(?m)^│ Deploy\s+app-0\s`))
	sfYAML      = All(Contains("apiVersion: v1"), Contains("kind: Pod"))
	sfInspector = All(Regexp("pods panel", `╭─ Pods (\S+ )?─`), Not(Contains("apiVersion: v1")))
)

func safinaScript() Script {
	return Script{
		// The context comes from --context; safina then shows the namespace
		// list. All user namespaces is the first row, Pods the second type.
		Launch: func(r *Rec, start time.Time) error {
			return recordLaunch(r, start, sfNamespaces, func() (time.Time, error) {
				if _, err := r.s.Do([]Key{"enter"}, sfTypes, r.timeout); err != nil {
					return time.Time{}, err
				}
				t0 := time.Now()
				st, err := r.s.Do([]Key{"j", "enter"}, sfPods, r.timeout)
				return t0.Add(st.Latency), err
			})
		},
		Ops: map[string]func(*Rec) error{
			opCursor: func(r *Rec) error { return r.MeasureEach(opCursor, repeat("j", cursorPresses)) },
			opFilterKey: func(r *Rec) error {
				if err := r.Go([]Key{"/"}, Contains("│/")); err != nil {
					return err
				}
				if err := r.MeasureEach(opFilterKey, Keys(filterTerm)); err != nil {
					return err
				}
				return r.Go([]Key{"esc"}, sfPodList)
			},
			opFilter: func(r *Rec) error {
				return r.Reps(func() error {
					if err := r.Measure(opFilter, Keys("/"+filterTerm), TopRows(sfPodRowRe, filterTerm, filterMatches)); err != nil {
						return err
					}
					if err := r.Go([]Key{"enter"}, Not(Contains("│/"+filterTerm))); err != nil {
						return err
					}
					return r.Go([]Key{"esc"}, sfPodList)
				})
			},
			opYAML: func(r *Rec) error {
				return r.Reps(func() error {
					if err := r.Measure(opYAML, []Key{"d"}, sfYAML); err != nil {
						return err
					}
					return r.Measure(opYAMLClose, []Key{"esc"}, sfInspector)
				})
			},
			opSwitch: func(r *Rec) error {
				return r.Reps(func() error {
					// esc leaves the pods list for the type list, cursor on Pods.
					if err := r.Go([]Key{"esc"}, sfTypes); err != nil {
						return err
					}
					if err := r.Go([]Key{"k"}, Differs(r.s.Styled())); err != nil {
						return err
					}
					if err := r.Measure(opSwitch, []Key{"enter"}, sfWorkloads); err != nil {
						return err
					}
					if err := r.Go([]Key{"esc"}, sfTypes); err != nil {
						return err
					}
					if err := r.Go([]Key{"j"}, Differs(r.s.Styled())); err != nil {
						return err
					}
					return r.Measure(opSwitchBk, []Key{"enter"}, sfPodList)
				})
			},
			opLogs: func(r *Rec) error {
				if err := r.Go(Keys("/chatter"), TopRows(sfPodRowRe, "chatter", 1)); err != nil {
					return err
				}
				if err := r.Go([]Key{"enter"}, Not(Contains("│/chatter"))); err != nil {
					return err
				}
				err := r.Reps(func() error {
					if err := r.Measure(opLogs, []Key{"l"}, Contains(logLine)); err != nil {
						return err
					}
					return r.Go([]Key{"esc"}, All(sfInspector, Not(Contains(logLine))))
				})
				if err != nil {
					return err
				}
				return r.Go([]Key{"esc"}, sfPodList)
			},
		},
	}
}

// --- k9s --------------------------------------------------------------------

var (
	k9PodRowRe = `^│ bench-[a-z0-9]+\s+([a-z]+(?:-\d+)?)-[0-9a-z]+-[0-9a-z]{5}\s`
	k9Pods     = All(Regexp("pods title", `pods\(all\)\[\d+\]`), Regexp("pod rows", `(?m)^│ bench-0\s+app-0-`))
	k9Deploys  = All(Regexp("deployments title", `deployments\(all\)\[\d+\]`), Regexp("deployment rows", `(?m)^│ bench-0\s+app-0\s`))
	k9YAML     = All(Contains("YAML("), Contains("apiVersion: v1"), Contains("kind: Pod"))
	k9List     = All(Regexp("pods title", `pods\(all\)\[\d+\]`), Not(Contains("YAML(")))
	k9PodList  = All(Regexp("pods title", `pods\(all\)\[\d+\]`), Regexp("pod rows", `(?m)^│ bench-\d+\s+app-\d+-`), Not(Contains("</")))
)

func k9sScript() Script {
	return Script{
		// --command pods --all-namespaces opens the pod list directly, so
		// first data and the pod list are the same frame.
		Launch: func(r *Rec, start time.Time) error {
			return recordLaunch(r, start, k9Pods, func() (time.Time, error) {
				return start.Add(r.lastStep(opFirstData).Latency), nil
			})
		},
		Ops: map[string]func(*Rec) error{
			opCursor: func(r *Rec) error { return r.MeasureEach(opCursor, repeat("j", cursorPresses)) },
			opFilterKey: func(r *Rec) error {
				if err := r.Go([]Key{"/"}, Contains("/")); err != nil {
					return err
				}
				if err := r.MeasureEach(opFilterKey, Keys(filterTerm)); err != nil {
					return err
				}
				return r.Go([]Key{"esc"}, k9PodList)
			},
			// k9s applies a filter on Enter, so Enter is part of typing it.
			opFilter: func(r *Rec) error {
				return r.Reps(func() error {
					keys := append(Keys("/"+filterTerm), "enter")
					if err := r.Measure(opFilter, keys, TopRows(k9PodRowRe, filterTerm, filterMatches)); err != nil {
						return err
					}
					return r.Go([]Key{"esc"}, k9PodList)
				})
			},
			opYAML: func(r *Rec) error {
				return r.Reps(func() error {
					if err := r.Measure(opYAML, []Key{"y"}, k9YAML); err != nil {
						return err
					}
					return r.Measure(opYAMLClose, []Key{"esc"}, k9List)
				})
			},
			opSwitch: func(r *Rec) error {
				return r.Reps(func() error {
					if err := r.Go(Keys(":deploy"), Contains("> deploy")); err != nil {
						return err
					}
					if err := r.Measure(opSwitch, []Key{"enter"}, k9Deploys); err != nil {
						return err
					}
					if err := r.Go(Keys(":pods"), Contains("> pods")); err != nil {
						return err
					}
					return r.Measure(opSwitchBk, []Key{"enter"}, k9PodList)
				})
			},
			opLogs: func(r *Rec) error {
				if err := r.Go(append(Keys("/chatter"), "enter"), TopRows(k9PodRowRe, "chatter", 1)); err != nil {
					return err
				}
				err := r.Reps(func() error {
					if err := r.Measure(opLogs, []Key{"l"}, Contains(logLine)); err != nil {
						return err
					}
					return r.Go([]Key{"esc"}, All(Regexp("pods title", `pods\(all\)\[\d+\]`), Not(Contains(logLine))))
				})
				if err != nil {
					return err
				}
				return r.Go([]Key{"esc"}, k9PodList)
			},
		},
	}
}

func repeat(k Key, n int) []Key {
	keys := make([]Key, n)
	for i := range keys {
		keys[i] = k
	}
	return keys
}
