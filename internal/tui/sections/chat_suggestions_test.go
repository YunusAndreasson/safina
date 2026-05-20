package sections

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yunus/safina/internal/k8s"
)

// TestChatSuggestionsForRef pins the kind×health → prompt-list matrix. The
// map-table idiom (vs slice-of-struct) doubles the key as the subtest name
// and randomises iteration order — if a future change introduces order
// coupling between cases the failure surfaces quickly.
func TestChatSuggestionsForRef(t *testing.T) {
	cases := map[string]struct {
		ref      NodeRef
		wantLen  int
		wantHead string // first prompt; empty means "expect nil"
	}{
		"pod_failing returns 3 starter prompts": {
			ref:      NodeRef{Kind: k8s.KindPod, Health: k8s.HealthFailing},
			wantLen:  3,
			wantHead: "why is this pod not starting?",
		},
		"pod_degraded returns 2 prompts": {
			ref:      NodeRef{Kind: k8s.KindPod, Health: k8s.HealthDegraded},
			wantLen:  2,
			wantHead: "what does this status mean?",
		},
		"pod_in_progress returns 1 prompt": {
			ref:      NodeRef{Kind: k8s.KindPod, Health: k8s.HealthInProgress},
			wantLen:  1,
			wantHead: "what is this pod waiting on?",
		},
		"pod_healthy returns no prompts": {
			ref: NodeRef{Kind: k8s.KindPod, Health: k8s.HealthHealthy},
		},
		"app_failing returns 3 prompts": {
			ref:      NodeRef{Kind: k8s.KindApp, Health: k8s.HealthFailing},
			wantLen:  3,
			wantHead: "why aren't replicas ready?",
		},
		"app_degraded returns 2 prompts": {
			ref:      NodeRef{Kind: k8s.KindApp, Health: k8s.HealthDegraded},
			wantLen:  2,
			wantHead: "is the HPA pinned at max?",
		},
		"app_in_progress returns rollout prompt": {
			ref:      NodeRef{Kind: k8s.KindApp, Health: k8s.HealthInProgress},
			wantLen:  1,
			wantHead: "how is the rollout going?",
		},
		"node_failing returns 2 prompts": {
			ref:      NodeRef{Kind: k8s.KindNode, Health: k8s.HealthFailing},
			wantLen:  2,
			wantHead: "why is this node not ready?",
		},
		"nodepool_degraded shares node prompts": {
			ref:      NodeRef{Kind: k8s.KindNodePool, Health: k8s.HealthDegraded},
			wantLen:  2,
			wantHead: "why is this node not ready?",
		},
		"node_healthy returns no prompts": {
			ref: NodeRef{Kind: k8s.KindNode, Health: k8s.HealthHealthy},
		},
		"context returns first-time-user nudge": {
			ref:      NodeRef{Kind: k8s.KindContext, Health: k8s.HealthHealthy},
			wantLen:  1,
			wantHead: "what's unhealthy in this cluster?",
		},
		"unknown kind returns no prompts": {
			ref: NodeRef{Kind: "Mystery", Health: k8s.HealthFailing},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := ChatSuggestionsForRef(tc.ref)
			if tc.wantHead == "" {
				assert.Nil(t, got, "healthy/unknown branch must return nil, not empty slice")
				return
			}
			assert.Len(t, got, tc.wantLen)
			assert.Equal(t, tc.wantHead, got[0])
		})
	}
}

// TestChatSuggestionsForRef_HardCap defends Hick's Law: we explicitly
// capped prompts at 3 per branch. A drive-by edit that appends a 4th
// prompt without thinking about scan cost should fail this guard.
func TestChatSuggestionsForRef_HardCap(t *testing.T) {
	probes := []NodeRef{
		{Kind: k8s.KindPod, Health: k8s.HealthFailing},
		{Kind: k8s.KindPod, Health: k8s.HealthDegraded},
		{Kind: k8s.KindPod, Health: k8s.HealthInProgress},
		{Kind: k8s.KindApp, Health: k8s.HealthFailing},
		{Kind: k8s.KindApp, Health: k8s.HealthDegraded},
		{Kind: k8s.KindApp, Health: k8s.HealthInProgress},
		{Kind: k8s.KindNode, Health: k8s.HealthFailing},
		{Kind: k8s.KindNodePool, Health: k8s.HealthFailing},
		{Kind: k8s.KindContext},
	}
	for _, ref := range probes {
		got := ChatSuggestionsForRef(ref)
		assert.LessOrEqual(t, len(got), 3, "ChatSuggestionsForRef must hard-cap at 3 entries: kind=%s health=%v returned %d", ref.Kind, ref.Health, len(got))
	}
}
