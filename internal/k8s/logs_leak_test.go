package k8s

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestStreamPodContainerLogs_NoGoroutineLeakOnDrop verifies the streamer
// goroutine returns once the consumer drains its channel after dropping
// interest — the historical pattern was: receiver sets logCh = nil, sender
// tries `ch <- Done` and blocks forever because nobody is reading.
//
// We can't test the receiver-side drain in isolation without a real REST
// pipe (the fake clientset's GetLogs returns a fixture stream that the
// streamer happily drains), so this test is mostly a regression marker that
// the streamer pattern still terminates predictably under cancel.
func TestStreamPodContainerLogs_NoGoroutineLeakOnDrop(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
	})

	baseline := goroutineCount()

	for i := 0; i < 30; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		ch := make(chan PodLogEvent)
		go StreamPodContainerLogs(ctx, cs, "ns", "p", "", false, ch)

		// Drain until the streamer closes the channel (which it does via
		// `defer close(ch)`).
		drainDone := make(chan struct{})
		go func() {
			for range ch {
			}
			close(drainDone)
		}()

		// Brief settle, then cancel.
		time.Sleep(2 * time.Millisecond)
		cancel()

		select {
		case <-drainDone:
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d: streamer did not close channel within 2s after cancel", i)
		}
	}

	// Allow the runtime a brief window to reap the now-finished goroutines.
	settle := time.Now().Add(2 * time.Second)
	for time.Now().Before(settle) {
		if goroutineCount() <= baseline+2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	got := goroutineCount()
	if got > baseline+2 {
		t.Errorf("goroutines leaked: baseline=%d after_30_iter=%d (delta=%d)\n%s",
			baseline, got, got-baseline, dumpGoroutines())
	}
}

func goroutineCount() int { return runtime.NumGoroutine() }

func dumpGoroutines() string {
	buf := make([]byte, 1<<16)
	n := runtime.Stack(buf, true)
	out := string(buf[:n])
	// Trim past the first 60 stacks worth — the message just needs to point
	// at the leakers, not paste the entire trace.
	if i := indexNthLine(out, 200); i > 0 {
		out = out[:i] + "\n... (truncated)\n"
	}
	return out
}

func indexNthLine(s string, n int) int {
	count := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			count++
			if count == n {
				return i
			}
		}
	}
	return -1
}

func init() {
	// Silence linters about the unused import in some build modes.
	_ = strings.TrimSpace
}
