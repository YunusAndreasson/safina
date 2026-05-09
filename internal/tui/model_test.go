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

	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/components"
	"github.com/yunus/safina/internal/tui/sections"
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
			assert.Equal(t, "…", string(got[len(got)-3:]))
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

func TestIsCtxDone(t *testing.T) {
	assert.True(t, isCtxDone(context.Canceled))
	assert.True(t, isCtxDone(context.DeadlineExceeded))
	assert.True(t, isCtxDone(fmt.Errorf("wrap: %w", context.Canceled)))
	assert.False(t, isCtxDone(errors.New("context canceled")))
	assert.False(t, isCtxDone(errors.New("some random error")))
	assert.False(t, isCtxDone(nil))
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

func TestClearBanner_ResetsFields(t *testing.T) {
	m := setupModel()
	m.raiseBanner("temp", false)
	assert.NotEmpty(t, m.bannerMsg)

	m.clearBanner()
	assert.Empty(t, m.bannerMsg)
	assert.False(t, m.bannerErr)
	assert.Equal(t, uint64(2), m.bannerEpoch)
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
	m.bannerErr = true

	m.Update(clearBannerMsg{epoch: 5})
	assert.Empty(t, m.bannerMsg)
	assert.False(t, m.bannerErr)
}

func TestBannerEpoch_OldTimerDoesNotWipeNewBanner(t *testing.T) {
	m := setupModel()
	m.raiseBanner("first", false)   // epoch=1
	m.raiseBanner("second", false)  // epoch=2

	// Simulate old timer firing (epoch=1)
	m.Update(clearBannerMsg{epoch: 1})
	assert.Equal(t, "second", m.bannerMsg,
		"old timer (epoch 1) should not wipe banner set at epoch 2")
	assert.False(t, m.bannerErr)
}

func TestRaiseError_Convenience(t *testing.T) {
	m := setupModel()
	cmd := m.raiseError("fail!")
	assert.NotNil(t, cmd)
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

func TestHandleConfirm_UpperCaseY_Confirms(t *testing.T) {
	m := setupModel()
	m.state = stateDeleteConfirm
	cmd := m.handleConfirm(tea.KeyPressMsg{Text: "Y", Code: 'Y'})
	assert.Nil(t, cmd)
	assert.Equal(t, stateMain, m.state)
}

// ── Scale input edge cases ───────────────────────────────────────────

func TestHandleScaleInput_NonDigitBlocked(t *testing.T) {
	m := setupModel()
	m.state = stateScaleInput
	_, cmd := m.handleScaleInput(tea.KeyPressMsg{Text: "a", Code: 'a'})
	assert.Nil(t, cmd)
}

func TestHandleScaleInput_EscCancels(t *testing.T) {
	m := setupModel()
	m.state = stateScaleInput
	_, cmd := m.handleScaleInput(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Nil(t, cmd)
	assert.Equal(t, stateMain, m.state)
}

