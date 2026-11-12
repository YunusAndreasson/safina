package tui

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
)

// ── Pure helpers ──────────────────────────────────────────────────────

func TestPlural(t *testing.T) {
	assert.Equal(t, "0 contexts", plural(0, "context"))
	assert.Equal(t, "1 context", plural(1, "context"))
	assert.Equal(t, "2 contexts", plural(2, "context"))
	assert.Equal(t, "5 errors", plural(5, "error"))
}

func TestFormatSkippedContexts(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		sc := []k8s.SkippedContext{
			{Name: "prod-cluster", Err: errors.New("connection refused")},
		}
		got := formatSkippedContexts(sc)
		assert.Contains(t, got, "prod-cluster")
		assert.Contains(t, got, "connection refused")
		assert.NotContains(t, got, "contexts")
	})

	t.Run("multiple", func(t *testing.T) {
		sc := []k8s.SkippedContext{
			{Name: "prod-cluster", Err: errors.New("refused")},
			{Name: "staging", Err: errors.New("timeout")},
			{Name: "dev", Err: errors.New("bad cert")},
		}
		got := formatSkippedContexts(sc)
		assert.Contains(t, got, "skipped")
		assert.Contains(t, got, "prod-cluster")
		assert.Contains(t, got, "2 others")
	})

	t.Run("two_contexts", func(t *testing.T) {
		sc := []k8s.SkippedContext{
			{Name: "prod", Err: errors.New("err1")},
			{Name: "staging", Err: errors.New("err2")},
		}
		got := formatSkippedContexts(sc)
		assert.Contains(t, got, "2 contexts")
		assert.Contains(t, got, "prod")
		assert.Contains(t, got, "1 other")
	})
}

func TestWindowTitleFor(t *testing.T) {
	t.Run("picker", func(t *testing.T) {
		assert.Equal(t, "safina", windowTitleFor(nil))
		assert.Equal(t, "safina", windowTitleFor([]components.Crumb{
			{Label: "Clusters"},
		}))
	})

	t.Run("cluster_drill", func(t *testing.T) {
		crumbs := []components.Crumb{
			{Label: "Clusters"},
			{Label: "prod"},
			{Label: "default"},
		}
		got := windowTitleFor(crumbs)
		assert.Contains(t, got, "safina")
		assert.Contains(t, got, "prod")
		assert.Contains(t, got, "default")
	})

	t.Run("truncation", func(t *testing.T) {
		crumbs := []components.Crumb{
			{Label: "Clusters"},
			{Label: "very-long-cluster-name-that-makes-the-title-really-long"},
			{Label: "some-very-long-namespace-name-for-testing-truncation"},
			{Label: "and-a-resource-name-to-really-push-it-over-the-edge"},
		}
		got := windowTitleFor(crumbs)
		assert.LessOrEqual(t, len(got), 60, "should be truncated to max 60")
		if len(got) >= 3 {
			assert.Equal(t, "…", got[len(got)-3:])
		}
	})

	t.Run("empty_labels_in_crumbs", func(t *testing.T) {
		crumbs := []components.Crumb{
			{Label: ""},
			{Label: "prod"},
		}
		got := windowTitleFor(crumbs)
		assert.Contains(t, got, "prod")
		assert.NotContains(t, got, "··") // empty labels should be skipped
	})
}

func TestActionSuccessMsg(t *testing.T) {
	assert.Equal(t, "done", actionSuccessMsg("", ""))
	assert.Equal(t, "scaled", actionSuccessMsg("scaled", ""))
	assert.Equal(t, "deleted · my-pod", actionSuccessMsg("deleted", "my-pod"))
	assert.Equal(t, "restarted · nginx/default", actionSuccessMsg("restarted", "nginx/default"))
}

func TestIsCanceled(t *testing.T) {
	assert.True(t, isCanceled(context.Canceled))
	assert.True(t, isCanceled(fmt.Errorf("wrap: %w", context.Canceled)))
	// A timeout is the cluster not answering, not the user moving on.
	assert.False(t, isCanceled(context.DeadlineExceeded))
	assert.False(t, isCanceled(fmt.Errorf("dial tcp 34.91.196.24:443: %w", context.DeadlineExceeded)))
	assert.False(t, isCanceled(errors.New("context canceled")))
	assert.False(t, isCanceled(errors.New("some random error")))
	assert.False(t, isCanceled(nil))
}

// ── Banner system ────────────────────────────────────────────────────

func setupModel() *Model {
	return &Model{bannerMsg: "", bannerErr: false, bannerEpoch: 0}
}

func TestRaiseBanner_BumpsEpoch(t *testing.T) {
	m := setupModel()
	cmd := m.raiseBanner("hello", false)
	require.NotNil(t, cmd)
	assert.Equal(t, uint64(1), m.bannerEpoch)
	assert.Equal(t, "hello", m.bannerMsg)
	assert.False(t, m.bannerErr)
}

func TestRaiseBanner_ErrorFlag(t *testing.T) {
	m := setupModel()
	m.raiseBanner("something broke", true)
	assert.True(t, m.bannerErr)
	assert.Equal(t, "something broke", m.bannerMsg)
}

func TestClearBannerMsg_StaleEpochNoOps(t *testing.T) {
	m := setupModel()
	m.bannerEpoch = 5
	m.bannerMsg = "current"
	m.bannerErr = true

	m.Update(clearBannerMsg{epoch: 3})
	assert.Equal(t, "current", m.bannerMsg)
	assert.True(t, m.bannerErr)
}

func TestClearBannerMsg_MatchingEpochClears(t *testing.T) {
	m := setupModel()
	m.bannerEpoch = 5
	m.bannerMsg = "current"
	m.bannerErr = false

	m.Update(clearBannerMsg{epoch: 5})
	assert.Empty(t, m.bannerMsg)
	assert.False(t, m.bannerErr)
}

func TestBannerEpoch_OldTimerDoesNotWipeNewBanner(t *testing.T) {
	m := setupModel()
	m.raiseBanner("first", false)  // epoch=1
	m.raiseBanner("second", false) // epoch=2

	// Simulate old timer firing (epoch=1)
	m.Update(clearBannerMsg{epoch: 1})
	assert.Equal(t, "second", m.bannerMsg,
		"old timer (epoch 1) should not wipe banner set at epoch 2")
	assert.False(t, m.bannerErr)
}

func TestRaiseError_Convenience(t *testing.T) {
	m := setupModel()
	cmd := m.raiseError("fail!")
	assert.Nil(t, cmd, "errors have no dismissal timer")
	assert.Equal(t, "fail!", m.bannerMsg)
	assert.True(t, m.bannerErr)
}

func TestRaiseStatus_Convenience(t *testing.T) {
	m := setupModel()
	cmd := m.raiseStatus("done")
	assert.NotNil(t, cmd)
	assert.Equal(t, "done", m.bannerMsg)
	assert.False(t, m.bannerErr)
}

// ── Clock ─────────────────────────────────────────────────────────────

func TestClockTick(t *testing.T) {
	m := setupModel()
	cmd := m.clockTick()
	assert.NotNil(t, cmd)

	msg := cmd()
	clock, ok := msg.(clockMsg)
	assert.True(t, ok)
	assert.False(t, time.Time(clock).IsZero())
}

// ── Auto-refresh ──────────────────────────────────────────────────────

func TestAutoRefreshTick(t *testing.T) {
	m := setupModel()
	cmd := m.autoRefreshTick()
	assert.NotNil(t, cmd)

	msg := cmd()
	_, ok := msg.(refreshMsg)
	assert.True(t, ok)
}

// ── SetStartupBanner ──────────────────────────────────────────────────

func TestSetStartupBanner(t *testing.T) {
	m := setupModel()
	m.SetStartupBanner("invalid --context flag")
	assert.Equal(t, "invalid --context flag", m.startupBanner)
}

// ── Log/Shell drain guards ────────────────────────────────────────────

func TestNextLogLine_NilChannel(t *testing.T) {
	m := setupModel()
	m.logCh = nil
	cmd := m.nextLogLine()
	assert.Nil(t, cmd)
}

func TestNextShellEvent_NilSession(t *testing.T) {
	m := setupModel()
	m.shellSession = nil
	cmd := m.nextShellEvent()
	assert.Nil(t, cmd)
}

// ── childCount ────────────────────────────────────────────────────────

func TestChildCount(t *testing.T) {
	assert.Equal(t, 0, childCount(nil))
	assert.Equal(t, 3, childCount(&sections.Children{
		Kind:     k8s.KindContextList,
		Contexts: []config.ContextInfo{{}, {}, {}},
	}))
	assert.Equal(t, 2, childCount(&sections.Children{
		Kind: k8s.KindApp,
		Apps: []k8s.AppInfo{{}, {}},
	}))
	assert.Equal(t, 0, childCount(&sections.Children{Kind: "bogus"}))
}

// ── Confirm dialog not in wrong state ─────────────────────────────────

func TestHandleConfirm_WrongStateDoesNothing(t *testing.T) {
	m := setupModel()
	// In stateMain (not deleteConfirm), 'y' should do nothing
	cmd := m.handleConfirm(tea.KeyPressMsg{Text: "y", Code: 'y'})
	assert.Nil(t, cmd)
	assert.Equal(t, stateMain, m.state)
}

func TestHandleConfirm_N_Cancels(t *testing.T) {
	m := setupModel()
	m.state = stateDeleteConfirm
	cmd := m.handleConfirm(tea.KeyPressMsg{Text: "n", Code: 'n'})
	assert.Nil(t, cmd)
	assert.Equal(t, stateMain, m.state)
}

func TestHandleConfirm_Esc_Cancels(t *testing.T) {
	m := setupModel()
	m.state = stateDeleteConfirm
	cmd := m.handleConfirm(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Nil(t, cmd)
	assert.Equal(t, stateMain, m.state)
}

// y shows YAML everywhere else, so in a confirm it does nothing: a habit
// must not write to the cluster.
func TestHandleConfirm_YDoesNotConfirm(t *testing.T) {
	for _, k := range []string{"y", "Y"} {
		m := setupModel()
		m.state = stateDeleteConfirm
		cmd := m.handleConfirm(tea.KeyPressMsg{Text: k, Code: rune(k[0])})
		assert.Nil(t, cmd)
		assert.Equal(t, stateDeleteConfirm, m.state, k)
	}
}

// Cancel has the focus first: Enter alone cancels, Tab then Enter acts.
func TestHandleConfirm_EnterChoosesTheFocusedButton(t *testing.T) {
	m := setupModel()
	m.state = stateDeleteConfirm
	assert.Nil(t, m.handleConfirm(tea.KeyPressMsg{Code: tea.KeyEnter}))
	assert.Equal(t, stateMain, m.state, "Enter on the default focus cancels")

	m.state = stateRestartConfirm
	m.handleConfirm(tea.KeyPressMsg{Code: tea.KeyTab})
	assert.True(t, m.confirmOnAction)
	m.handleConfirm(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, stateMain, m.state)
	assert.False(t, m.confirmOnAction, "the next dialog starts on Cancel again")
}

// ── Scale input edge cases ───────────────────────────────────────────

func TestHandleScaleInput_NonDigitBlocked(t *testing.T) {
	m := setupModel()
	m.state = stateScaleInput
	_, cmd := m.handleScaleInput(tea.KeyPressMsg{Text: "a", Code: 'a'})
	assert.Nil(t, cmd)
}

// ── Edit session lifecycle ────────────────────────────────────────────

func TestEditSession_LineDiffStat(t *testing.T) {
	tests := []struct {
		name              string
		orig, edited      string
		wantAdd, wantRemv int
	}{
		{"identical", "a\nb\nc\n", "a\nb\nc\n", 0, 0},
		{"pure add", "a\nb\n", "a\nb\nc\n", 1, 0},
		{"pure remove", "a\nb\nc\n", "a\nc\n", 0, 1},
		{"replace", "a\nb\nc\n", "a\nB\nc\n", 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			es := &editSession{Orig: []byte(tt.orig), Edited: []byte(tt.edited)}
			add, rem := es.LineDiffStat()
			assert.Equal(t, tt.wantAdd, add)
			assert.Equal(t, tt.wantRemv, rem)
		})
	}
}

func TestEditSession_LineDiffStat_NilSafe(t *testing.T) {
	var es *editSession
	add, rem := es.LineDiffStat()
	assert.Equal(t, 0, add)
	assert.Equal(t, 0, rem)
}

func TestHandleConfirm_EditCancelClearsSession(t *testing.T) {
	m := setupModel()
	m.state = stateEditConfirm
	m.edit = &editSession{Kind: "ConfigMap", Name: "x", Namespace: "ns"}
	cmd := m.handleConfirm(tea.KeyPressMsg{Text: "n", Code: 'n'})
	assert.Nil(t, cmd)
	assert.Equal(t, stateMain, m.state)
	assert.Nil(t, m.edit, "edit session should be cleared on cancel")
}

func TestEditKindFor(t *testing.T) {
	assert.Equal(t, k8s.EditKindConfigMap, editKindFor(sections.NodeRef{Kind: k8s.KindConfigMap}))
	assert.Equal(t, k8s.EditKindSecret, editKindFor(sections.NodeRef{Kind: k8s.KindSecret}))
	assert.Equal(t, k8s.EditKindDeployment, editKindFor(sections.NodeRef{Kind: k8s.KindApp, SubKind: k8s.AppKindDeployment}))
	assert.Equal(t, k8s.EditKindStatefulSet, editKindFor(sections.NodeRef{Kind: k8s.KindApp, SubKind: k8s.AppKindStatefulSet}))
	assert.Equal(t, k8s.EditKindDaemonSet, editKindFor(sections.NodeRef{Kind: k8s.KindApp, SubKind: k8s.AppKindDaemonSet}))
	// CronJob/Job stay excluded — Jobs are immutable post-create.
	assert.Empty(t, editKindFor(sections.NodeRef{Kind: k8s.KindApp, SubKind: k8s.AppKindCronJob}))
	assert.Empty(t, editKindFor(sections.NodeRef{Kind: k8s.KindApp, SubKind: k8s.AppKindJob}))
	assert.Empty(t, editKindFor(sections.NodeRef{Kind: k8s.KindPod}))
	assert.Empty(t, editKindFor(sections.NodeRef{Kind: k8s.KindNode}))
}

func TestActionTarget(t *testing.T) {
	cases := []struct {
		name     string
		ref      sections.NodeRef
		wantKind string
		wantNS   string
		wantName string
	}{
		// Apps return their k8s subkind so DescribeYAML/Events get the
		// real API kind name (Deployment / StatefulSet / …).
		{"deployment", sections.NodeRef{Kind: k8s.KindApp, SubKind: k8s.AppKindDeployment, Namespace: "ns", Name: "api"}, "Deployment", "ns", "api"},
		{"statefulset", sections.NodeRef{Kind: k8s.KindApp, SubKind: k8s.AppKindStatefulSet, Namespace: "ns", Name: "db"}, "StatefulSet", "ns", "db"},
		{"daemonset", sections.NodeRef{Kind: k8s.KindApp, SubKind: k8s.AppKindDaemonSet, Namespace: "ns", Name: "agent"}, "DaemonSet", "ns", "agent"},
		{"cronjob", sections.NodeRef{Kind: k8s.KindApp, SubKind: k8s.AppKindCronJob, Namespace: "ns", Name: "nightly"}, "CronJob", "ns", "nightly"},
		{"job", sections.NodeRef{Kind: k8s.KindApp, SubKind: k8s.AppKindJob, Namespace: "ns", Name: "migrate"}, "Job", "ns", "migrate"},

		{"pod", sections.NodeRef{Kind: k8s.KindPod, Namespace: "ns", Name: "p"}, "Pod", "ns", "p"},
		{"node", sections.NodeRef{Kind: k8s.KindNode, Name: "n1"}, "Node", "", "n1"},
		{"namespace", sections.NodeRef{Kind: k8s.KindNamespace, Name: "shop"}, "Namespace", "", "shop"},

		// Leaf resources — these were silently broken before. Verify
		// they now translate to the expected k8s API kinds.
		{"configmap", sections.NodeRef{Kind: k8s.KindConfigMap, Namespace: "ns", Name: "cfg"}, "ConfigMap", "ns", "cfg"},
		{"secret", sections.NodeRef{Kind: k8s.KindSecret, Namespace: "ns", Name: "tls"}, "Secret", "ns", "tls"},
		{"service", sections.NodeRef{Kind: k8s.KindService, Namespace: "ns", Name: "api"}, "Service", "ns", "api"},
		{"ingress", sections.NodeRef{Kind: k8s.KindIngress, Namespace: "ns", Name: "web"}, "Ingress", "ns", "web"},
		{"pvc", sections.NodeRef{Kind: k8s.KindPVC, Namespace: "ns", Name: "data"}, "PersistentVolumeClaim", "ns", "data"},
		{"pv", sections.NodeRef{Kind: k8s.KindPV, Name: "vol"}, "PersistentVolume", "", "vol"},

		// Container has no API object of its own; the handler does the
		// parent-pod indirection. actionTarget returns empty here on
		// purpose so the handler's special-case is the only path.
		{"container", sections.NodeRef{Kind: k8s.KindContainer, Namespace: "ns", Name: "c"}, "", "", ""},

		// Unsupported kinds return empty so the handler bails.
		{"clusterfold", sections.NodeRef{Kind: k8s.KindClusterFold}, "", "", ""},
		{"contextlist", sections.NodeRef{Kind: k8s.KindContextList}, "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gk, gns, gn := actionTarget(c.ref)
			assert.Equal(t, c.wantKind, gk)
			assert.Equal(t, c.wantNS, gns)
			assert.Equal(t, c.wantName, gn)
		})
	}
}

func TestResolveEditor_DefaultsToVi(t *testing.T) {
	t.Setenv("KUBE_EDITOR", "")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	bin, args := resolveEditor()
	assert.Equal(t, "vi", bin)
	assert.Empty(t, args)
}

func TestResolveEditor_RespectsKubeEditor(t *testing.T) {
	t.Setenv("KUBE_EDITOR", "code --wait")
	t.Setenv("EDITOR", "vim")
	bin, args := resolveEditor()
	assert.Equal(t, "code", bin)
	assert.Equal(t, []string{"--wait"}, args)
}

func TestHandleScaleInput_EscCancels(t *testing.T) {
	m := setupModel()
	m.state = stateScaleInput
	_, cmd := m.handleScaleInput(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Nil(t, cmd)
	assert.Equal(t, stateMain, m.state)
}

// ── Confirm-state escape contract ─────────────────────────────────────
//
// update.go dispatches seven different confirm states (Delete, Rollback,
// Restart, RestartPod, Edit, Cordon, Drain) to the same handleConfirm
// path. Today only Delete + Edit cancellation are covered. If a future
// refactor adds an eighth state but forgets to wire it into
// handleConfirm — or breaks one of the existing five — esc would
// silently stop returning the operator to stateMain and they'd be stuck
// inside a modal with no obvious way out. This table pins all seven.

func TestHandleConfirm_EscFromEveryConfirmState_ReturnsToMain(t *testing.T) {
	cases := map[string]state{
		"delete":      stateDeleteConfirm,
		"rollback":    stateRollbackConfirm,
		"restart":     stateRestartConfirm,
		"restart_pod": stateRestartPodConfirm,
		"edit":        stateEditConfirm,
		"cordon":      stateCordonConfirm,
		"drain":       stateDrainConfirm,
	}
	for name, st := range cases {
		t.Run(name, func(t *testing.T) {
			m := setupModel()
			m.state = st
			cmd := m.handleConfirm(tea.KeyPressMsg{Code: tea.KeyEscape})
			assert.Nil(t, cmd, "esc must not emit a command")
			assert.Equal(t, stateMain, m.state, "esc must return to stateMain from %v", st)
		})
	}
}

func TestHandleConfirm_NFromEveryConfirmState_ReturnsToMain(t *testing.T) {
	// Same contract, lowercase n. Cancel verb parity matters because the
	// modal footer teaches "y/n" — if n stops working in (say) the Drain
	// modal, the operator must escape the entire app to get out.
	cases := map[string]state{
		"delete":      stateDeleteConfirm,
		"rollback":    stateRollbackConfirm,
		"restart":     stateRestartConfirm,
		"restart_pod": stateRestartPodConfirm,
		"edit":        stateEditConfirm,
		"cordon":      stateCordonConfirm,
		"drain":       stateDrainConfirm,
	}
	for name, st := range cases {
		t.Run(name, func(t *testing.T) {
			m := setupModel()
			m.state = st
			cmd := m.handleConfirm(tea.KeyPressMsg{Text: "n", Code: 'n'})
			assert.Nil(t, cmd, "n must not emit a command")
			assert.Equal(t, stateMain, m.state, "n must return to stateMain from %v", st)
		})
	}
}

// TestHandleConfirm_EditEscClearsSession pins that *esc* (not just 'n')
// runs cleanupEdit. The existing TestHandleConfirm_EditCancelClearsSession
// only covers the 'n' path; an asymmetry where esc skipped the cleanup
// would leak a tempfile and leave m.edit pointing at stale bytes that
// would re-appear if the user re-entered the edit flow.
func TestHandleConfirm_EditEscClearsSession(t *testing.T) {
	m := setupModel()
	m.state = stateEditConfirm
	m.edit = &editSession{Kind: "ConfigMap", Name: "x", Namespace: "ns"}
	cmd := m.handleConfirm(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Nil(t, cmd)
	assert.Equal(t, stateMain, m.state)
	assert.Nil(t, m.edit, "esc from stateEditConfirm must run cleanupEdit (parity with 'n')")
}

// ── Scale input sequence ─────────────────────────────────────────────
//
// Tests that drive the scale input through multiple Update calls and
// assert the running state evolves correctly. These probe routing +
// handler interaction in a way the existing single-message tests don't.

func TestHandleScaleInput_DigitsAccepted_NonDigitsRejected_EscRestoresMain(t *testing.T) {
	m := setupModel()
	m.state = stateScaleInput

	// Digit accepted — handler routes through bubble's textinput Update
	// and returns its cmd (timer / blink — non-nil is fine, we don't
	// assert the cmd shape here, only state continuity).
	_, _ = m.handleScaleInput(tea.KeyPressMsg{Text: "5", Code: '5'})
	assert.Equal(t, stateScaleInput, m.state, "digit must keep us in scale-input mode")

	// Non-digit dropped silently — no state change, no command.
	_, cmd := m.handleScaleInput(tea.KeyPressMsg{Text: "a", Code: 'a'})
	assert.Nil(t, cmd, "non-digit input must be silently dropped")
	assert.Equal(t, stateScaleInput, m.state, "non-digit must NOT bounce out of scale-input mode")

	// Esc returns to main.
	_, cmd = m.handleScaleInput(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Nil(t, cmd)
	assert.Equal(t, stateMain, m.state, "esc must return to stateMain")
}

// ── Banner sequence ──────────────────────────────────────────────────

// TestBannerSequence_NewerRaiseEclipsesOlderTimer drives the full
// "banner A → banner B → A's stale timer fires → B is still up" flow
// as a sequence. Individual pieces are tested above; this one pins the
// composition.
func TestBannerSequence_NewerRaiseEclipsesOlderTimer(t *testing.T) {
	m := setupModel()

	// Raise A at epoch=1.
	m.raiseBanner("A", false)
	require.Equal(t, "A", m.bannerMsg)
	epochA := m.bannerEpoch

	// Raise B at epoch=2. Banner now reads B.
	m.raiseBanner("B", true) // error-style banner
	require.Equal(t, "B", m.bannerMsg)
	require.True(t, m.bannerErr)
	require.Greater(t, m.bannerEpoch, epochA)

	// A's stale timer fires. Must not clobber B.
	m.Update(clearBannerMsg{epoch: epochA})
	assert.Equal(t, "B", m.bannerMsg, "stale timer A must not wipe banner B")
	assert.True(t, m.bannerErr, "B's error flag must survive A's timer")

	// Errors survive timers until explicitly dismissed.
	m.Update(clearBannerMsg{epoch: m.bannerEpoch})
	assert.Equal(t, "B", m.bannerMsg)
	m.dismissBanner()
	assert.Empty(t, m.bannerMsg)
	assert.False(t, m.bannerErr)
}

func TestHandleScaleInputRejectsIntegerOverflow(t *testing.T) {
	for _, input := range []string{"2147483648", "9223372036854775808"} {
		t.Run(input, func(t *testing.T) {
			m := setupModel()
			m.state = stateScaleInput
			m.scaleInput.SetValue(input)
			_, cmd := m.handleScaleInput(tea.KeyPressMsg{Code: tea.KeyEnter})
			require.Nil(t, cmd)
			assert.Equal(t, stateScaleInput, m.state)
			assert.Contains(t, m.scaleError, "whole number")
			assert.Equal(t, input, m.scaleInput.Value())
		})
	}
}
