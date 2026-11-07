package k8s

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePortPair(t *testing.T) {
	cases := []struct {
		in            string
		local, remote int
		wantErr       bool
	}{
		{"8080:80", 8080, 80, false},
		{"8080", 8080, 8080, false}, // single = same on both sides
		{"  443:8443  ", 443, 8443, false},
		{"0:80", 0, 0, true}, // 0 is reserved
		{"65535:1", 65535, 1, false},
		{"65536:1", 0, 0, true},
		{"abc:80", 0, 0, true},
		{"80:", 0, 0, true},
		{":80", 0, 0, true},
		{"", 0, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			l, r, err := ParsePortPair(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.local, l)
			assert.Equal(t, tc.remote, r)
		})
	}
}

func TestPickFreeLocalPort(t *testing.T) {
	p, err := PickFreeLocalPort(t.Context())
	require.NoError(t, err)
	assert.Positive(t, p)
	assert.LessOrEqual(t, p, 65535)
}

func TestForwardManager_Count_StartsEmpty(t *testing.T) {
	fm := NewForwardManager()
	assert.Equal(t, 0, fm.Count())
	assert.Empty(t, fm.List())
}

func TestForwardManager_Stop_UnknownID(t *testing.T) {
	fm := NewForwardManager()
	err := fm.Stop("nope")
	require.Error(t, err)
}

// ── Goroutine lifecycle contract ─────────────────────────────────────
//
// StartPortForward can't be exercised here without a live SPDY server,
// but the *contract* it relies on is "Stop closes pf.stop, which causes
// the forwarder goroutine to exit." A simulated forwarder lets us pin
// that contract — and goleak.VerifyTestMain in setup_test.go upgrades
// any regression into a suite-level test failure rather than a silent
// process-wide leak.

// fakeForwarder spawns a goroutine that mirrors what StartPortForward
// does: block on pf.stop, mark exited when the channel closes.
func fakeForwarder(t *testing.T, pf *PortForward, exited *atomic.Bool) {
	t.Helper()
	go func() {
		<-pf.stop
		exited.Store(true)
	}()
}

func TestForwardManager_Stop_ReleasesForwarderGoroutine(t *testing.T) {
	fm := NewForwardManager()
	pf := pinForward(fm, "f1", time.Now(), 8080)

	var exited atomic.Bool
	fakeForwarder(t, pf, &exited)

	require.NoError(t, fm.Stop("f1"))

	// Tight wait — fakeForwarder reacts as soon as pf.stop closes. Don't
	// use a long sleep; that would mask a genuine leak by always passing.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if exited.Load() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("fakeForwarder did not exit within 500ms of Stop — close(pf.stop) is not signalling")
}

func TestForwardManager_StopAll_ReleasesAllForwarderGoroutines(t *testing.T) {
	fm := NewForwardManager()
	exited := make([]*atomic.Bool, 5)
	for i := range exited {
		pf := pinForward(fm, idFor(i), time.Now().Add(time.Duration(i)*time.Millisecond), 9000+i)
		exited[i] = &atomic.Bool{}
		fakeForwarder(t, pf, exited[i])
	}

	fm.StopAll()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		done := true
		for _, e := range exited {
			if !e.Load() {
				done = false
				break
			}
		}
		if done {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	for i, e := range exited {
		assert.True(t, e.Load(), "fakeForwarder %d did not exit after StopAll", i)
	}
}

// ── Concurrency invariants ───────────────────────────────────────────
//
// These tests are designed to be run under -race. They hammer the
// ForwardManager mutex with parallel Stop/List/Count calls, and assert
// end-state invariants (no leaked entries, no vanished entries).

func TestForwardManager_ConcurrentStopAndList(t *testing.T) {
	fm := NewForwardManager()

	const N = 50
	ids := make([]string, N)
	for i := range N {
		ids[i] = idFor(i)
		pinForward(fm, ids[i], time.Now().Add(time.Duration(i)*time.Microsecond), 8000+i)
	}

	var wg sync.WaitGroup
	// Half the goroutines call Stop on disjoint IDs; the other half hammer
	// List/Count. Disjoint Stops keep the test focused on the read/write
	// race rather than double-stop semantics (covered separately).
	for i := range N / 2 {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_ = fm.Stop(id)
		}(ids[i])
	}
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				_ = fm.List()
				_ = fm.Count()
			}
		}()
	}
	wg.Wait()

	// Exactly N/2 forwards remain (the ones we didn't Stop). A mutex bug
	// would either drop entries (count < N/2) or leak them (count > N/2).
	require.Equal(t, N/2, fm.Count(), "exactly the unstopped half must remain")
	assert.Len(t, fm.List(), N/2)
}

// TestForwardManager_List_StaleSignalSurvivesConcurrentList probes the
// errCh re-buffer trick in List(): it drains the channel to check for an
// error, then puts the error back. Under concurrent List calls, two
// goroutines could race to drain — if either misses the re-buffer, the
// stale signal vanishes. This test pins that at least one observer sees
// staleness AND a follow-up sequential List still sees it.
func TestForwardManager_List_StaleSignalSurvivesConcurrentList(t *testing.T) {
	fm := NewForwardManager()
	pf := pinForward(fm, "f1", time.Now(), 8080)
	pf.errCh <- &simpleError{s: "pod went away"}

	var staleA, staleB bool
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if l := fm.List(); len(l) == 1 {
			staleA = l[0].Stale
		}
	}()
	go func() {
		defer wg.Done()
		if l := fm.List(); len(l) == 1 {
			staleB = l[0].Stale
		}
	}()
	wg.Wait()

	assert.True(t, staleA || staleB,
		"at least one of two concurrent List calls must observe stale=true (errCh staleness must not be lost to the race)")

	final := fm.List()
	require.Len(t, final, 1)
	assert.True(t, final[0].Stale, "stale must persist across subsequent List calls")
}

// idFor returns a short stable id for the concurrent tests so the test
// body reads cleanly without inline hex.
func idFor(i int) string {
	const digits = "0123456789ABCDEF"
	return "f" + string(digits[i/16]) + string(digits[i%16])
}
