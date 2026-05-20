package testutil

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// NewClientset returns a fake.NewSimpleClientset preloaded with objs.
// Wrapper exists to centralize this idiom — every k8s test was rolling
// its own `fake.NewSimpleClientset(...)` call, which made it hard to
// evolve test setup (e.g. defaulting reactors, tracking timestamps)
// without touching dozens of files.
func NewClientset(tb testing.TB, objs ...runtime.Object) kubernetes.Interface {
	tb.Helper()
	return fake.NewSimpleClientset(objs...)
}
