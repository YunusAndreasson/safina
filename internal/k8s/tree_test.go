package k8s

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// ── Health Glyph ──────────────────────────────────────────────────────

func TestHealth_Glyph(t *testing.T) {
	assert.Equal(t, "✗", HealthFailing.Glyph())
	assert.Equal(t, "⚠", HealthDegraded.Glyph())
	assert.Equal(t, "⏵", HealthInProgress.Glyph())
	assert.Equal(t, "✓", HealthHealthy.Glyph())
	assert.Equal(t, "…", HealthUnknown.Glyph())
	assert.Equal(t, "…", Health(99).Glyph())
}

func TestHealth_Worse(t *testing.T) {
	assert.Equal(t, HealthFailing, HealthHealthy.Worse(HealthFailing))
	assert.Equal(t, HealthFailing, HealthFailing.Worse(HealthHealthy))
	assert.Equal(t, HealthDegraded, HealthHealthy.Worse(HealthDegraded))
	assert.Equal(t, HealthHealthy, HealthHealthy.Worse(HealthHealthy))
}

// ── classifyDeploymentHealth ──────────────────────────────────────────

func TestClassifyDeploymentHealth(t *testing.T) {
	three := int32(3)
	zero := int32(0)

	tests := []struct {
		name string
		d    appsv1.Deployment
		want Health
	}{
		{
			name: "all_ready_and_updated",
			d: appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{Replicas: &three},
				Status: appsv1.DeploymentStatus{
					ReadyReplicas:   3,
					UpdatedReplicas: 3,
					Replicas:        3,
				},
			},
			want: HealthHealthy,
		},
		{
			name: "scaled_to_zero",
			d: appsv1.Deployment{
				Spec:   appsv1.DeploymentSpec{Replicas: &zero},
				Status: appsv1.DeploymentStatus{},
			},
			want: HealthUnknown,
		},
		{
			name: "replica_failure_condition",
			d: appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{Replicas: &three},
				Status: appsv1.DeploymentStatus{
					Conditions: []appsv1.DeploymentCondition{
						{Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionTrue},
					},
				},
			},
			want: HealthFailing,
		},
		{
			name: "progress_deadline_exceeded",
			d: appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{Replicas: &three},
				Status: appsv1.DeploymentStatus{
					Conditions: []appsv1.DeploymentCondition{
						{Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse},
					},
				},
			},
			want: HealthFailing,
		},
		{
			name: "unavailable_but_some_ready_is_degraded",
			d: appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{Replicas: &three},
				Status: appsv1.DeploymentStatus{
					ReadyReplicas:       1,
					UpdatedReplicas:     2,
					Replicas:            3,
					UnavailableReplicas: 2,
				},
			},
			want: HealthDegraded,
		},
		{
			name: "unavailable_zero_ready_is_inprogress",
			d: appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{Replicas: &three},
				Status: appsv1.DeploymentStatus{
					ReadyReplicas:       0,
					UpdatedReplicas:     2,
					Replicas:            3,
					UnavailableReplicas: 3,
				},
			},
			want: HealthInProgress,
		},
		{
			name: "fresh_rollout_in_progress",
			d: appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{Replicas: &three},
				Status: appsv1.DeploymentStatus{
					ReadyReplicas:   0,
					UpdatedReplicas: 1,
					Replicas:        1,
				},
			},
			want: HealthInProgress,
		},
		{
			name: "nil_replicas_treated_as_zero",
			d: appsv1.Deployment{
				Spec:   appsv1.DeploymentSpec{Replicas: nil},
				Status: appsv1.DeploymentStatus{},
			},
			want: HealthUnknown,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyDeploymentHealth(tt.d)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ── classifyWorkloadHealth ────────────────────────────────────────────

func TestClassifyWorkloadHealth(t *testing.T) {
	tests := []struct {
		name        string
		ready       int32
		desired     int32
		unavailable bool
		want        Health
	}{
		{"all_ready", 3, 3, false, HealthHealthy},
		{"all_ready_unavailable_ignored", 3, 3, true, HealthHealthy},
		{"zero_desired", 0, 0, false, HealthUnknown},
		{"zero_ready_failing", 0, 3, false, HealthFailing},
		{"partial_ready_in_progress", 1, 3, false, HealthInProgress},
		{"unavailable_with_some_ready_degraded", 2, 5, true, HealthDegraded},
		{"unavailable_zero_ready_failing", 0, 5, true, HealthFailing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyWorkloadHealth(tt.ready, tt.desired, tt.unavailable)
			assert.Equal(t, tt.want, got, "ready=%d desired=%d unavailable=%v", tt.ready, tt.desired, tt.unavailable)
		})
	}
}

// ── ContainerHealth ───────────────────────────────────────────────────

func TestContainerHealth(t *testing.T) {
	tests := []struct {
		state string
		want  Health
	}{
		{"Running", HealthHealthy},
		{"Terminated:Completed", HealthHealthy},
		{"Waiting:CrashLoopBackOff", HealthFailing},
		{"Waiting:ImagePullBackOff", HealthFailing},
		{"Waiting:ErrImagePull", HealthFailing},
		{"Terminated:OOMKilled", HealthFailing},
		{"Terminated:Error", HealthFailing},
		{"Waiting:ContainerCreating", HealthInProgress},
		{"Waiting:PodInitializing", HealthInProgress},
		{"Waiting:SomeUnknownReason", HealthUnknown},
		{"", HealthUnknown},
		{"Terminated:SomeOtherExit", HealthUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			ci := ContainerInfo{State: tt.state}
			got := ContainerHealth(ci)
			assert.Equal(t, tt.want, got, "state=%q", tt.state)
		})
	}
}

// ── PodHealth ─────────────────────────────────────────────────────────

func TestPodHealth(t *testing.T) {
	tests := []struct {
		name   string
		pod    PodInfo
		want   Health
	}{
		{"running_healthy", PodInfo{Status: "Running", Restarts: 0}, HealthHealthy},
		{"running_high_restarts_degraded", PodInfo{Status: "Running", Restarts: 10}, HealthDegraded},
		{"running_just_below_threshold", PodInfo{Status: "Running", Restarts: 9}, HealthHealthy},
		{"succeeded_healthy", PodInfo{Status: "Succeeded"}, HealthHealthy},
		{"pending_in_progress", PodInfo{Status: "Pending"}, HealthInProgress},
		{"container_creating_in_progress", PodInfo{Status: "ContainerCreating"}, HealthInProgress},
		{"terminating_in_progress", PodInfo{Status: "Terminating"}, HealthInProgress},
		{"failed_failing", PodInfo{Status: "Failed"}, HealthFailing},
		{"crashloop_failing", PodInfo{Status: "CrashLoopBackOff"}, HealthFailing},
		{"imagepull_failing", PodInfo{Status: "ImagePullBackOff"}, HealthFailing},
		{"errimagepull_failing", PodInfo{Status: "ErrImagePull"}, HealthFailing},
		{"error_failing", PodInfo{Status: "Error"}, HealthFailing},
		{"unknown_status", PodInfo{Status: "Bogus"}, HealthUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PodHealth(tt.pod)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ── Namespace classification ──────────────────────────────────────────

func TestIsSystemNamespace(t *testing.T) {
	system := []string{
		"kube-system", "kube-public", "kube-node-lease",
		"calico-system", "istio-system", "gke-system",
		"cert-manager", "ingress-nginx", "gatekeeper-system",
		"metallb-system", "external-secrets", "velero",
		"kyverno", "tigera-operator", "tekton-pipelines",
		"monitoring", "logging", "gmp-system",
	}
	for _, ns := range system {
		assert.True(t, IsSystemNamespace(ns), "expected system: %s", ns)
	}
	user := []string{"default", "my-app", "prod-api", "staging"}
	for _, ns := range user {
		assert.False(t, IsSystemNamespace(ns), "expected user: %s", ns)
	}
}

func TestClassifyNamespaces(t *testing.T) {
	all := []string{"default", "kube-system", "my-app", "monitoring", "staging", "cert-manager"}

	t.Run("no_overrides", func(t *testing.T) {
		user, sys := ClassifyNamespaces(all, nil, nil)
		userNames := namesTo(user)
		sysNames := namesTo(sys)
		assert.Equal(t, []string{"default", "my-app", "staging"}, userNames)
		assert.Equal(t, []string{"cert-manager", "kube-system", "monitoring"}, sysNames)
	})

	t.Run("cfgUser_empty_means_no_override", func(t *testing.T) {
		// Empty cfgUser should NOT act as "allow everything" — it means "use default classification"
		user, sys := ClassifyNamespaces(all, nil, nil)
		assert.Len(t, user, 3)
		assert.Len(t, sys, 3)
	})

	t.Run("cfgUser_allowlist_overrides_everything", func(t *testing.T) {
		user, sys := ClassifyNamespaces(all, []string{"default", "kube-system"}, nil)
		userNames := namesTo(user)
		sysNames := namesTo(sys)
		assert.Equal(t, []string{"default", "kube-system"}, userNames)
		assert.Equal(t, []string{"cert-manager", "monitoring", "my-app", "staging"}, sysNames)
	})

	t.Run("cfgSystem_overrides_to_system", func(t *testing.T) {
		user, sys := ClassifyNamespaces(all, nil, []string{"default", "staging"})
		userNames := namesTo(user)
		sysNames := namesTo(sys)
		assert.Equal(t, []string{"my-app"}, userNames)
		assert.Equal(t, []string{"cert-manager", "default", "kube-system", "monitoring", "staging"}, sysNames)
	})

	t.Run("cfgUser_takes_precedence_over_cfgSystem", func(t *testing.T) {
		// When cfgUser is non-empty, cfgSystem is ignored
		user, sys := ClassifyNamespaces(all, []string{"default"}, []string{"kube-system"})
		userNames := namesTo(user)
		for _, n := range userNames {
			assert.Equal(t, "default", n)
		}
		// everything else is system
		assert.Len(t, sys, 5)
	})

	t.Run("empty_input", func(t *testing.T) {
		user, sys := ClassifyNamespaces(nil, nil, nil)
		assert.Empty(t, user)
		assert.Empty(t, sys)
	})

	t.Run("output_sorted", func(t *testing.T) {
		user, sys := ClassifyNamespaces([]string{"z-ns", "a-ns"}, nil, nil)
		if len(user) > 0 {
			for i := 1; i < len(user); i++ {
				assert.True(t, user[i-1].Name < user[i].Name, "user not sorted: %v", namesTo(user))
			}
		}
		for i := 1; i < len(sys); i++ {
			assert.True(t, sys[i-1].Name < sys[i].Name, "system not sorted: %v", namesTo(sys))
		}
	})
}

func namesTo(infos []NamespaceInfo) []string {
	out := make([]string, len(infos))
	for i, ni := range infos {
		out[i] = ni.Name
	}
	return out
}

// ── jobToApp ──────────────────────────────────────────────────────────

func TestJobToApp(t *testing.T) {
	t.Run("completed_job", func(t *testing.T) {
		completions := int32(1)
		j := batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: "done", Namespace: "ns",
				CreationTimestamp: metav1.NewTime(time.Now())},
			Spec: batchv1.JobSpec{Completions: &completions},
			Status: batchv1.JobStatus{Succeeded: 1},
		}
		a := jobToApp(j, nil, nil)
		assert.Equal(t, AppKindJob, a.Kind)
		assert.Equal(t, "1/1", a.Ready)
		assert.Equal(t, HealthHealthy, a.Health)
	})

	t.Run("failed_job", func(t *testing.T) {
		completions := int32(3)
		j := batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: "fail", Namespace: "ns",
				CreationTimestamp: metav1.NewTime(time.Now())},
			Spec:   batchv1.JobSpec{Completions: &completions},
			Status: batchv1.JobStatus{Succeeded: 1, Failed: 2},
		}
		a := jobToApp(j, nil, nil)
		assert.Equal(t, HealthFailing, a.Health)
	})

	t.Run("active_job_with_no_succeeded", func(t *testing.T) {
		completions := int32(5)
		j := batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: "active", Namespace: "ns",
				CreationTimestamp: metav1.NewTime(time.Now())},
			Spec:   batchv1.JobSpec{Completions: &completions},
			Status: batchv1.JobStatus{Active: 3, Succeeded: 0},
		}
		a := jobToApp(j, nil, nil)
		// Succeeded=0, falls back to Active=3: shows "3/5"
		assert.Equal(t, "3/5", a.Ready)
		assert.Equal(t, HealthInProgress, a.Health)
	})

	t.Run("no_completions_no_parallelism_defaults_desired_to_1", func(t *testing.T) {
		j := batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: "simple", Namespace: "ns",
				CreationTimestamp: metav1.NewTime(time.Now())},
			Spec:   batchv1.JobSpec{}, // neither Completions nor Parallelism set
			Status: batchv1.JobStatus{Active: 1},
		}
		a := jobToApp(j, nil, nil)
		assert.Equal(t, "1/1", a.Ready)
	})

	t.Run("suspended_cronjob_is_degraded", func(t *testing.T) {
		suspend := true
		c := batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{Name: "suspended", Namespace: "ns",
				CreationTimestamp: metav1.NewTime(time.Now())},
			Spec:   batchv1.CronJobSpec{Suspend: &suspend},
			Status: batchv1.CronJobStatus{},
		}
		a := cronJobToApp(c)
		assert.Equal(t, HealthDegraded, a.Health)
	})

	t.Run("cronjob_with_last_schedule", func(t *testing.T) {
		lastTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))
		c := batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{Name: "cj", Namespace: "ns",
				CreationTimestamp: metav1.NewTime(time.Now())},
			Spec:   batchv1.CronJobSpec{},
			Status: batchv1.CronJobStatus{LastScheduleTime: &lastTime},
		}
		a := cronJobToApp(c)
		assert.Equal(t, HealthHealthy, a.Health)
		assert.Contains(t, a.NextSchedule, "last")
	})
}

// ── jobOwnedByCronJob ─────────────────────────────────────────────────

func TestJobOwnedByCronJob(t *testing.T) {
	uid := metav1.NewTime(time.Now()).Time.Format("20060102")
	j := batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "CronJob", Name: "mycron", UID: types.UID(uid)},
			},
		},
	}
	assert.True(t, jobOwnedByCronJob(j))

	j2 := batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: "mydeploy"},
			},
		},
	}
	assert.False(t, jobOwnedByCronJob(j2))

	j3 := batchv1.Job{}
	assert.False(t, jobOwnedByCronJob(j3))
}

// ── podEffectiveRequests ──────────────────────────────────────────────

func TestPodEffectiveRequests(t *testing.T) {
	t.Run("single_container", func(t *testing.T) {
		p := corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				}},
			},
		}
		cpu, mem := podEffectiveRequests(p)
		assert.Equal(t, int64(100), cpu)
		assert.Equal(t, int64(134217728), mem) // 128 Mi in bytes
	})

	t.Run("multi_container_sum", func(t *testing.T) {
		p := corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "a", Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")},
					}},
					{Name: "b", Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("300m")},
					}},
				},
			},
		}
		cpu, _ := podEffectiveRequests(p)
		assert.Equal(t, int64(500), cpu)
	})

	t.Run("init_container_max_wins", func(t *testing.T) {
		p := corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
					},
				}},
				InitContainers: []corev1.Container{{
					Name: "init-db",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
					},
				}},
			},
		}
		cpu, _ := podEffectiveRequests(p)
		assert.Equal(t, int64(500), cpu, "init container max should override regular container sum")
	})

	t.Run("sidecar_init_adds_to_regular", func(t *testing.T) {
		restartAlways := corev1.ContainerRestartPolicyAlways
		p := corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")},
					},
				}},
				InitContainers: []corev1.Container{{
					Name:          "sidecar-proxy",
					RestartPolicy: &restartAlways,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
					},
				}},
			},
		}
		cpu, _ := podEffectiveRequests(p)
		assert.Equal(t, int64(300), cpu, "sidecar init should add to regular containers sum")
	})

	t.Run("overhead_added", func(t *testing.T) {
		p := corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")},
					},
				}},
				Overhead: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("50m"),
				},
			},
		}
		cpu, _ := podEffectiveRequests(p)
		assert.Equal(t, int64(250), cpu)
	})

	t.Run("no_requests_is_zero", func(t *testing.T) {
		p := corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "app"}},
			},
		}
		cpu, mem := podEffectiveRequests(p)
		assert.Equal(t, int64(0), cpu)
		assert.Equal(t, int64(0), mem)
	})
}

// ── sumPodRequests ────────────────────────────────────────────────────

func TestSumPodRequests(t *testing.T) {
	t.Run("terminating_pod_skipped", func(t *testing.T) {
		now := metav1.Now()
		pods := []corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "app",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU: resource.MustParse("1000m"),
							},
						},
					}},
				},
				Status: corev1.PodStatus{Phase: corev1.PodFailed},
			},
		}
		cpu, _, count := sumPodRequests(pods)
		assert.Equal(t, int64(0), cpu, "terminating+Failed pods should be skipped")
		assert.Equal(t, int32(0), count)
	})

	t.Run("terminating_succeeded_skipped", func(t *testing.T) {
		now := metav1.Now()
		pods := []corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "app",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
						},
					}},
				},
				Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
			},
		}
		cpu, _, _ := sumPodRequests(pods)
		assert.Equal(t, int64(0), cpu)
	})

	t.Run("terminating_running_still_counted", func(t *testing.T) {
		now := metav1.Now()
		pods := []corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "app",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")},
						},
					}},
				},
				Status: corev1.PodStatus{Phase: corev1.PodRunning},
			},
		}
		cpu, _, count := sumPodRequests(pods)
		assert.Equal(t, int64(100), cpu, "terminating but Running phase should still count")
		assert.Equal(t, int32(1), count)
	})

	t.Run("normal_pod_counted", func(t *testing.T) {
		pods := []corev1.Pod{
			{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "app",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("200m")},
						},
					}},
				},
			},
		}
		cpu, _, count := sumPodRequests(pods)
		assert.Equal(t, int64(200), cpu)
		assert.Equal(t, int32(1), count)
	})
}

// ── quantity helpers ──────────────────────────────────────────────────

func TestQuantityMilli(t *testing.T) {
	rl := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("1500m"),
		corev1.ResourceMemory: resource.MustParse("256Mi"),
	}
	assert.Equal(t, int64(1500), quantityMilli(rl, corev1.ResourceCPU))
	assert.Equal(t, int64(0), quantityMilli(rl, "nonexistent"))
	assert.Equal(t, int64(0), quantityMilli(nil, corev1.ResourceCPU))
}

func TestQuantityBytes(t *testing.T) {
	rl := corev1.ResourceList{
		corev1.ResourceMemory: resource.MustParse("256Mi"),
	}
	assert.Equal(t, int64(268435456), quantityBytes(rl, corev1.ResourceMemory))
	assert.Equal(t, int64(0), quantityBytes(rl, "nonexistent"))
}

// ── labelsMatch ───────────────────────────────────────────────────────

func TestLabelsMatch(t *testing.T) {
	podLabels := map[string]string{"app": "nginx", "version": "v1"}
	assert.True(t, labelsMatch(podLabels, map[string]string{"app": "nginx"}))
	assert.False(t, labelsMatch(podLabels, map[string]string{"app": "apache"}))
	assert.False(t, labelsMatch(podLabels, map[string]string{"app": "nginx", "tier": "frontend"}))
	assert.True(t, labelsMatch(podLabels, map[string]string{"app": "nginx", "version": "v1"}))
	assert.False(t, labelsMatch(podLabels, map[string]string{}))
	assert.False(t, labelsMatch(map[string]string{}, map[string]string{}))
}

// ── HPA badge ─────────────────────────────────────────────────────────

func TestHPAV2ToBadge(t *testing.T) {
	min := int32(2)
	h := autoscalingv2.HorizontalPodAutoscaler{
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MinReplicas: &min,
			MaxReplicas: 10,
		},
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{
			CurrentReplicas: 10,
		},
	}
	b := hpaV2ToBadge(h)
	assert.Equal(t, int32(2), b.Min)
	assert.Equal(t, int32(10), b.Max)
	assert.Equal(t, int32(10), b.Current)
	assert.True(t, b.PinnedAtMax)
}

func TestHPAV2ToBadge_NotPinned(t *testing.T) {
	min := int32(1)
	h := autoscalingv2.HorizontalPodAutoscaler{
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MinReplicas: &min,
			MaxReplicas: 10,
		},
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{
			CurrentReplicas: 5,
		},
	}
	b := hpaV2ToBadge(h)
	assert.False(t, b.PinnedAtMax)
}

func TestHPAV2ToBadge_NilMin(t *testing.T) {
	h := autoscalingv2.HorizontalPodAutoscaler{
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MinReplicas: nil,
			MaxReplicas: 5,
		},
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: 3},
	}
	b := hpaV2ToBadge(h)
	assert.Equal(t, int32(0), b.Min)
}

// ── Node classification ───────────────────────────────────────────────

func TestNodeReadyStatus(t *testing.T) {
	ready := corev1.Node{
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
	assert.Equal(t, "Ready", nodeReadyStatus(ready))

	notReady := corev1.Node{
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
			},
		},
	}
	assert.Equal(t, "NotReady", nodeReadyStatus(notReady))

	unknown := corev1.Node{Status: corev1.NodeStatus{}}
	assert.Equal(t, "Unknown", nodeReadyStatus(unknown))
}

func TestNodeHealth(t *testing.T) {
	tests := []struct {
		name string
		ni   NodeInfo
		want Health
	}{
		{"not_ready", NodeInfo{Status: "NotReady"}, HealthFailing},
		{"unschedulable_degraded", NodeInfo{Status: "Ready", Schedulable: false}, HealthDegraded},
		{"cpu_over_90_percent", NodeInfo{
			Status: "Ready", Schedulable: true,
			CPUAlloc: 4000, CPURequest: 3700,
		}, HealthDegraded},
		{"cpu_exactly_90_percent_not_degraded", NodeInfo{
			Status: "Ready", Schedulable: true,
			CPUAlloc: 4000, CPURequest: 3600,
		}, HealthHealthy},
		{"mem_over_90_percent", NodeInfo{
			Status: "Ready", Schedulable: true,
			MemAlloc: 8589934592, MemRequest: 7900000000, // > 90% of 8Gi
		}, HealthDegraded},
		{"healthy", NodeInfo{Status: "Ready", Schedulable: true}, HealthHealthy},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, nodeHealth(tt.ni))
		})
	}
}

func TestDetectNodePool(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		pid    string
		want   string
	}{
		{"karpenter", map[string]string{"karpenter.sh/nodepool": "default"}, "", "default"},
		{"gke_nodepool", map[string]string{"cloud.google.com/gke-nodepool": "pool-1"}, "", "pool-1"},
		{"eks_nodegroup", map[string]string{"eks.amazonaws.com/nodegroup": "ng-1"}, "", "ng-1"},
		{"aks_agentpool", map[string]string{"agentpool": "aks-pool"}, "", "aks-pool"},
		{"gce_provider_fallback", map[string]string{}, "gce://project/zone/instance", "gce"},
		{"aws_provider_fallback", map[string]string{}, "aws:///i-abc123", "aws"},
		{"azure_provider_fallback", map[string]string{}, "azure:///vm-name", "azure"},
		{"self_managed", map[string]string{}, "", "self-managed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Labels: tt.labels},
				Spec:       corev1.NodeSpec{ProviderID: tt.pid},
			}
			assert.Equal(t, tt.want, detectNodePool(n))
		})
	}
}

// ── Node roles ────────────────────────────────────────────────────────

func TestNodeRoles(t *testing.T) {
	empty := corev1.Node{}
	assert.Equal(t, "", nodeRoles(empty))

	master := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"node-role.kubernetes.io/control-plane": "",
				"node-role.kubernetes.io/master":        "",
			},
		},
	}
	roles := nodeRoles(master)
	assert.Contains(t, roles, "control-plane")
	assert.Contains(t, roles, "master")
}

// ── groupIntoPools ────────────────────────────────────────────────────

func TestGroupIntoPools(t *testing.T) {
	nodes := []NodeInfo{
		{Name: "n1", Pool: "pool-a", Health: HealthHealthy, CPUUsed: 100, CPUAlloc: 1000},
		{Name: "n2", Pool: "pool-a", Health: HealthFailing, CPUUsed: 200, CPUAlloc: 2000},
		{Name: "n3", Pool: "pool-b", Health: HealthDegraded, CPUUsed: 50, CPUAlloc: 500},
	}
	pools := groupIntoPools(nodes)
	require.Len(t, pools, 2)

	// Sorted by name
	assert.Equal(t, "pool-a", pools[0].Name)
	assert.Equal(t, "pool-b", pools[1].Name)

	// pool-a has 2 nodes, worst health = Failing (from n2)
	assert.Equal(t, 2, pools[0].Nodes)
	assert.Equal(t, HealthFailing, pools[0].Health)
	assert.Equal(t, int64(300), pools[0].CPUUsed)
	assert.Equal(t, int64(3000), pools[0].CPUAlloc)

	// pool-b has 1 node
	assert.Equal(t, 1, pools[1].Nodes)
	assert.Equal(t, HealthDegraded, pools[1].Health)
}

func TestGroupIntoPools_Empty(t *testing.T) {
	pools := groupIntoPools(nil)
	assert.Empty(t, pools)
}

// ── int32PtrVal ───────────────────────────────────────────────────────

func TestInt32PtrVal(t *testing.T) {
	assert.Equal(t, int32(0), int32PtrVal(nil))
	v := int32(5)
	assert.Equal(t, int32(5), int32PtrVal(&v))
}

// ── aggregateForSelector ──────────────────────────────────────────────

func TestAggregateForSelector(t *testing.T) {
	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "p1", Namespace: "ns",
				Labels: map[string]string{"app": "nginx"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "p2", Namespace: "ns",
				Labels: map[string]string{"app": "nginx"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "p3", Namespace: "other-ns",
				Labels: map[string]string{"app": "nginx"},
			},
		},
	}
	metrics := map[string]ResourceUsage{
		"ns/p1": {CPU: 100, Memory: 1000},
		"ns/p2": {CPU: 200, Memory: 2000},
		"other-ns/p3": {CPU: 999, Memory: 9999},
	}
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}}

	cpu, mem := aggregateForSelector("ns", sel, pods, metrics)
	assert.Equal(t, int64(300), cpu) // p1 + p2, not p3 (different ns)
	assert.Equal(t, int64(3000), mem)
}

func TestAggregateForSelector_NilSelector(t *testing.T) {
	cpu, mem := aggregateForSelector("ns", nil, nil, nil)
	assert.Equal(t, int64(0), cpu)
	assert.Equal(t, int64(0), mem)
}

func TestAggregateForSelector_NilMetrics(t *testing.T) {
	pods := []corev1.Pod{{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1", Namespace: "ns",
			Labels: map[string]string{"app": "nginx"},
		},
	}}
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "nginx"}}
	cpu, mem := aggregateForSelector("ns", sel, pods, nil)
	assert.Equal(t, int64(0), cpu)
	assert.Equal(t, int64(0), mem)
}

// ── makeContainerInfo ─────────────────────────────────────────────────

func TestMakeContainerInfo(t *testing.T) {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
	}
	c := corev1.Container{Name: "app", Image: "nginx:1.25"}
	st := corev1.ContainerStatus{
		Name:  "app",
		Ready: true,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	}
	ci := makeContainerInfo(p, c, "regular", st, nil)
	assert.Equal(t, "app", ci.Name)
	assert.Equal(t, "nginx:1.25", ci.Image)
	assert.Equal(t, "regular", ci.Kind)
	assert.Equal(t, "Running", ci.State)
	assert.True(t, ci.Ready)
}

func TestMakeContainerInfo_Waiting(t *testing.T) {
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"}}
	c := corev1.Container{Name: "app"}
	st := corev1.ContainerStatus{
		Name: "app",
		State: corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
		},
		LastTerminationState: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"},
		},
	}
	ci := makeContainerInfo(p, c, "regular", st, nil)
	assert.Equal(t, "Waiting:CrashLoopBackOff", ci.State)
	assert.Equal(t, "CrashLoopBackOff", ci.LastReason)
}

func TestMakeContainerInfo_Terminated(t *testing.T) {
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"}}
	c := corev1.Container{Name: "app"}
	st := corev1.ContainerStatus{
		Name: "app",
		State: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1},
		},
	}
	ci := makeContainerInfo(p, c, "regular", st, nil)
	assert.Equal(t, "Terminated:Error", ci.State)
	assert.Equal(t, "Error", ci.LastReason)
}

func TestMakeContainerInfo_LastReasonFromPreviousTermination(t *testing.T) {
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"}}
	c := corev1.Container{Name: "app"}
	st := corev1.ContainerStatus{
		Name:  "app",
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		LastTerminationState: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"},
		},
	}
	ci := makeContainerInfo(p, c, "regular", st, nil)
	assert.Equal(t, "OOMKilled", ci.LastReason)
}

func TestMakeContainerInfo_WithMetrics(t *testing.T) {
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"}}
	c := corev1.Container{Name: "app"}
	st := corev1.ContainerStatus{Name: "app"}
	usage := map[string]ResourceUsage{
		"app": {CPU: 250, Memory: 536870912},
	}
	ci := makeContainerInfo(p, c, "regular", st, usage)
	assert.Equal(t, int64(250), ci.CPU)
	assert.Equal(t, int64(536870912), ci.Memory)
}

// ── DaemonSet health with unavailable flag ───────────────────────────

func TestDaemonSetToApp_UnavailableIsDegraded(t *testing.T) {
	now := time.Now()
	d := appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "ds", Namespace: "ns",
			CreationTimestamp: metav1.NewTime(now)},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "ds"}},
		},
		Status: appsv1.DaemonSetStatus{
			DesiredNumberScheduled: 5,
			NumberReady:            3,
			NumberUnavailable:      2,
		},
	}
	a := daemonSetToApp(d, nil, nil)
	assert.Equal(t, "3/5", a.Ready)
	assert.Equal(t, HealthDegraded, a.Health)
}
