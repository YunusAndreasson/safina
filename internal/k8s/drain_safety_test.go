package k8s

import (
	"context"
	"testing"
	"time"

	"github.com/YunusAndreasson/safina/internal/history"
	"github.com/YunusAndreasson/safina/internal/k8s/testutil"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func drainTestClient(pod *corev1.Pod) *fake.Clientset {
	cs := fake.NewSimpleClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}, pod)
	cs.Resources = []*metav1.APIResourceList{{GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "pods/eviction", Kind: "Eviction", Group: "policy", Version: "v1"}}}}
	return cs
}

func TestDrainRejectsUnsafeSelectionBeforeCordon(t *testing.T) {
	cs := drainTestClient(testutil.Pod("unmanaged", testutil.WithOrphan()))
	err := DrainNode(t.Context(), cs, "n1", DefaultDrainOptions(), make(chan DrainEvent, 16))
	require.Error(t, err)
	for _, action := range cs.Actions() {
		require.Contains(t, []string{"get", "list"}, action.GetVerb())
	}
}

func TestDrainRefusesDeleteFallback(t *testing.T) {
	cs := drainTestClient(testutil.Pod("app"))
	cs.Resources = []*metav1.APIResourceList{{GroupVersion: "v1"}}
	err := DrainNode(t.Context(), cs, "n1", DefaultDrainOptions(), make(chan DrainEvent, 16))
	require.ErrorContains(t, err, "refusing direct pod deletion")
	for _, action := range cs.Actions() {
		require.Contains(t, []string{"get", "list"}, action.GetVerb())
	}
}

func TestDrainRechecksPodsAfterCordon(t *testing.T) {
	cs := drainTestClient(testutil.Pod("new-unmanaged", testutil.WithOrphan()))
	lists := 0
	cs.PrependReactor("list", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
		lists++
		if lists == 1 {
			return true, &corev1.PodList{}, nil
		}
		return false, nil, nil
	})
	require.Error(t, DrainNode(t.Context(), cs, "n1", DefaultDrainOptions(), make(chan DrainEvent, 16)))
	require.Equal(t, 2, lists)
	node, err := cs.CoreV1().Nodes().Get(t.Context(), "n1", metav1.GetOptions{})
	require.NoError(t, err)
	require.True(t, node.Spec.Unschedulable)
	for _, action := range cs.Actions() {
		require.NotEqual(t, "create", action.GetVerb(), "blocked drain must not evict")
	}
}

func TestDrainWaitsForDisappearance(t *testing.T) {
	for _, terminating := range []bool{false, true} {
		name := "accepted"
		if terminating {
			name = "terminating"
		}
		t.Run(name, func(t *testing.T) {
			pod := testutil.Pod("app")
			pod.UID = "original"
			if terminating {
				testutil.WithTerminating()(pod)
			}
			cs := drainTestClient(pod)
			attempts := make(chan ktesting.Action, 4)
			cs.PrependReactor("create", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
				attempts <- action
				return true, nil, nil // Acceptance is not disappearance.
			})
			opts := DefaultDrainOptions()
			opts.Timeout = 50 * time.Millisecond
			events := make(chan DrainEvent, 16)
			require.Error(t, DrainNode(t.Context(), cs, "n1", opts, events))
			require.NotEmpty(t, attempts)
			action := <-attempts
			require.Equal(t, "eviction", action.GetSubresource())
			create, ok := action.(ktesting.CreateAction)
			require.True(t, ok)
			eviction, ok := create.GetObject().(*policyv1.Eviction)
			require.True(t, ok)
			require.NotNil(t, eviction.DeleteOptions.Preconditions)
			require.Equal(t, pod.UID, *eviction.DeleteOptions.Preconditions.UID)
			node, err := cs.CoreV1().Nodes().Get(t.Context(), "n1", metav1.GetOptions{})
			require.NoError(t, err)
			require.True(t, node.Spec.Unschedulable)
			for event := range events {
				if event.Done {
					require.Error(t, event.Err)
				}
			}
		})
	}
}

func TestDrainCancellationKeepsNodeCordoned(t *testing.T) {
	cs := drainTestClient(testutil.Pod("app"))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cs.PrependReactor("create", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
		cancel()
		return true, nil, context.Canceled
	})
	require.Error(t, DrainNode(ctx, cs, "n1", DefaultDrainOptions(), make(chan DrainEvent, 16)))
	node, err := cs.CoreV1().Nodes().Get(t.Context(), "n1", metav1.GetOptions{})
	require.NoError(t, err)
	require.True(t, node.Spec.Unschedulable)
}

func TestDrainRetriesAreRecordedAndCompletionWaits(t *testing.T) {
	pod := testutil.Pod("app")
	pod.UID = "original"
	cs := drainTestClient(pod)
	store, err := history.Open(history.Options{Directory: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	op := store.Begin(history.Target{Cluster: "cluster", Kind: "Node", Name: "n1"}, "drain", nil)
	ctx := history.WithOperation(t.Context(), op)
	calls := 0
	cs.PrependReactor("create", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
		calls++
		if calls == 1 {
			return true, nil, apierrors.NewTooManyRequests("PDB blocked", 0)
		}
		deleteErr := cs.Tracker().Delete(corev1.SchemeGroupVersion.WithResource("pods"), pod.Namespace, pod.Name)
		return true, nil, deleteErr
	})
	helper := newDrainHelper(ctx, cs, DefaultDrainOptions())
	helper.Client = drainClient{Interface: cs, uids: map[types.NamespacedName]types.UID{{Namespace: "default", Name: "app"}: pod.UID}}
	helper.EvictErrorRetryDelay = time.Millisecond
	require.NoError(t, helper.DeleteOrEvictPods([]corev1.Pod{*pod}))
	require.Equal(t, 2, calls)
	require.NoError(t, store.Close())
	result, err := history.Query(store.Directory(), history.Filter{Resource: "Pod/app"})
	require.NoError(t, err)
	outcomes := []string{}
	for _, record := range result.Records {
		require.Equal(t, pod.UID, record.Target.UID)
		if record.Outcome != "started" {
			outcomes = append(outcomes, record.Outcome)
		}
	}
	require.Equal(t, []string{"requested", "failed", "requested", "accepted"}, outcomes)
}
