package sections

import "github.com/yunus/safina/internal/k8s"

// ChatSuggestionsForRef returns 0 to 3 starter prompts keyed off the current
// selection's (kind, health). Hick's Law: hard-cap the list. We only suggest
// when there's a real reason to — healthy resources don't get noisy chips.
//
// Returned strings are the literal prompts the user would have typed; the
// chat panel renders them as chips and on click feeds them straight through
// the same Send path (with the same auto-injected [Selection] block).
func ChatSuggestionsForRef(ref NodeRef) []string {
	switch ref.Kind {
	case k8s.KindPod:
		switch ref.Health {
		case k8s.HealthFailing:
			return []string{
				"why is this pod not starting?",
				"summarize the recent events",
				"what should I check next?",
			}
		case k8s.HealthDegraded:
			return []string{
				"what does this status mean?",
				"is a restart safe here?",
			}
		case k8s.HealthInProgress:
			return []string{
				"what is this pod waiting on?",
			}
		case k8s.HealthHealthy, k8s.HealthUnknown:
		}
	case k8s.KindApp:
		switch ref.Health {
		case k8s.HealthFailing:
			return []string{
				"why aren't replicas ready?",
				"what events explain this?",
				"safe rollback steps?",
			}
		case k8s.HealthDegraded:
			return []string{
				"is the HPA pinned at max?",
				"what's the right scale here?",
			}
		case k8s.HealthInProgress:
			return []string{
				"how is the rollout going?",
			}
		case k8s.HealthHealthy, k8s.HealthUnknown:
		}
	case k8s.KindNode, k8s.KindNodePool:
		switch ref.Health {
		case k8s.HealthFailing, k8s.HealthDegraded:
			return []string{
				"why is this node not ready?",
				"what's pressuring this node?",
			}
		case k8s.HealthInProgress, k8s.HealthHealthy, k8s.HealthUnknown:
		}
	case k8s.KindContext:
		// Cluster picker — no resource yet, but a good "where do I start?"
		// nudge for first-time users.
		return []string{
			"what's unhealthy in this cluster?",
		}
	}
	return nil
}
