package sections

import (
	"github.com/yunus/safina/internal/history"
	"github.com/yunus/safina/internal/k8s"
)

// Message types emitted across the sections package. The Cmd builders that
// produce these live in commands.go (and tree_children.go for the tree
// children loader).

// PodSelectedMsg is emitted when the user activates a pod (Enter / l).
type PodSelectedMsg struct {
	Name, Namespace, Container string
}

// TreeFocusChangedMsg is emitted by the Tree section after Push/Pop. The
// root model uses it to schedule a load for the new focus.
type TreeFocusChangedMsg struct {
	Path []NodeRef
}

// TreeChildrenLoadedMsg carries a freshly fetched payload for a tree level.
type TreeChildrenLoadedMsg struct {
	Path     []NodeRef
	Children *Children
	Err      error
}

// ActionDoneMsg is the result envelope for every mutating call we issue
// against the cluster (delete, scale, restart, rollback, …). Subject is the
// human label of the affected resource — surfaced in the success banner so
// the operator gets visible confirmation of what just happened. Action is
// the verb (must match the keymap label so the success banner reads in the
// same vocabulary the user pressed: Jakob's Law).
type WorkloadTarget struct{ Context, Namespace, Kind, Name string }

type ActionDoneMsg struct {
	Operation  *history.Operation
	Generation string
	Target     *WorkloadTarget
	Action     string
	Subject    string
	Err        error
}

type LogStreamStartedMsg struct {
	Err     error
	Request uint64
	Pod     string
	Ch      chan k8s.PodLogEvent
}

// EventsLoadedMsg is delivered when the events fetch completes for the
// resource currently shown in the events modal.
type EventsLoadedMsg struct {
	Events []k8s.EventInfo
	Err    error
}

// HotLoadedMsg carries the top-N pods sorted by CPU usage for the hot-list
// modal. Mirrors EventsLoadedMsg's shape so the same load-flow patterns
// apply: SetItems(items, err) on the section, done.
type HotLoadedMsg struct {
	Pods []k8s.PodInfo
	Err  error
}

// DescribeLoadedMsg carries the YAML dump for the resource currently shown
// in the describe modal.
type DescribeLoadedMsg struct {
	YAML string
	Err  error
}

// EditYAMLLoadedMsg is the result of fetching a resource's YAML for editing.
// The root model handles it by writing YAML to a tempfile and launching
// $EDITOR via tea.ExecProcess. Kind/Namespace/Name echo back so the next
// step (apply) doesn't have to re-resolve them from the tree.
type EditYAMLLoadedMsg struct {
	Kind, Namespace, Name string
	YAML                  string
	Err                   error
}

// EditorExitedMsg is emitted by the tea.ExecProcess callback once the user
// closes their editor. The model reads the tempfile, diffs it against the
// original, and either banners (no changes / parse error) or transitions to
// the edit-confirm modal.
type EditorExitedMsg struct {
	Err error
}

// ShellSessionStartedMsg is emitted once the remotecommand stream has been
// dialed. The Shell section attaches to Session and the root pump-loop
// starts pulling ShellEvent messages from it.
type ShellSessionStartedMsg struct {
	Operation *history.Operation
	Session   *k8s.ShellSession
	IsDebug   bool // true when this came from AttachDebug (netshoot fall-back)
}

// ShellOutputMsg carries one chunk of stdout/stderr bytes from the running
// shell. The section feeds it into the vt10x emulator.
type ShellOutputMsg struct {
	Bytes []byte
}

// ShellExitMsg is emitted when the shell session ends — either cleanly
// (user typed `exit`) or via an error. NoShell signals the distroless /
// scratch case where we should offer the netshoot debug-pod fall-back.
// Bytes carries any final buffered output that was greedy-drained alongside
// the Done event in the same Cmd; the consumer ingests Bytes before
// processing the exit so the pod's last words aren't dropped.
type ShellExitMsg struct {
	Bytes   []byte
	Err     error
	NoShell bool
}

// DiscoveryLoadedMsg carries the list of GVRs the cluster exposes,
// populated once per cluster switch (lazy on first open of the Resource
// Browser). The root model stores the list on the Tree so future fold
// rendering can use real Kind names from discovery.
type DiscoveryLoadedMsg struct {
	Kinds []k8s.ResourceKind
	Err   error
}
