package bench

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

// fakeClusterWith returns a fake clientset seeded with the canonical bench
// topology (10 namespaces × 5 deployments + a few pods/services per ns).
// Used to exercise scenarios offline.
func fakeClusterWith(t *testing.T) *fake.Clientset {
	t.Helper()
	objs := make([]runtime.Object, 0, 160)
	for i := range 10 {
		ns := "bench-" + itoa(i)
		objs = append(objs, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
		for d := range 5 {
			objs = append(objs,
				&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
					Name: "app-" + itoa(d), Namespace: ns,
				}},
				&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
					Name: "app-" + itoa(d) + "-pod", Namespace: ns,
				}, Status: corev1.PodStatus{Phase: corev1.PodRunning}},
				&corev1.Service{ObjectMeta: metav1.ObjectMeta{
					Name: "svc-" + itoa(d), Namespace: ns,
				}},
			)
		}
	}
	return fake.NewSimpleClientset(objs...)
}

func TestScenario_ColdListPods_Baseline(t *testing.T) {
	cs := fakeClusterWith(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := runColdListPods(ctx, cs, &Options{})
	require.NoError(t, err)
}

func TestScenario_ColdListPods_FieldSelector(t *testing.T) {
	cs := fakeClusterWith(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// V4: field selector path doesn't error (fake clientset ignores the
	// selector, but the code path must not panic / mis-merge).
	err := runColdListPods(ctx, cs, &Options{UseFieldSelector: true})
	require.NoError(t, err)
}

func TestScenario_ClusterHealth_Baseline(t *testing.T) {
	cs := fakeClusterWith(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, runClusterHealth(ctx, cs, &Options{}))
}

func TestScenario_ResourcesPerNS_BoundedFanout(t *testing.T) {
	cs := fakeClusterWith(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// V8 (fanout=4) must produce same result as unbounded.
	err := runResourcesPerNS(ctx, cs, &Options{FanoutLimit: 4})
	require.NoError(t, err)
}

func TestMergeFieldSelector(t *testing.T) {
	assert.Equal(t, "a=b", mergeFieldSelector("", "a=b"))
	assert.Equal(t, "x=y,a=b", mergeFieldSelector("x=y", "a=b"))
}

func TestMakeSem(t *testing.T) {
	assert.Nil(t, makeSem(0))
	assert.Nil(t, makeSem(-1))
	s := makeSem(4)
	assert.NotNil(t, s)
	assert.Equal(t, 4, cap(s))
}

func TestAllVariantsResolve(t *testing.T) {
	for _, id := range AllVariants() {
		_, err := Lookup(id)
		assert.NoError(t, err, "variant %s must resolve", id)
	}
}

func TestAllScenariosResolve(t *testing.T) {
	for _, id := range AllScenarios() {
		_, err := LookupScenario(id)
		assert.NoError(t, err, "scenario %s must resolve", id)
	}
}

func TestLookup_Unknown(t *testing.T) {
	_, err := Lookup(VariantID("nope"))
	require.Error(t, err)
	_, err = LookupScenario(ScenarioID("nope"))
	require.Error(t, err)
}

// itoa avoids strconv import noise in test fixtures.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
