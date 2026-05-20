package testutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// fixtureDir resolves the testdata directory relative to this source file
// so callers can be in any package — `go test` runs with the importing
// package's directory as CWD, which is unhelpful for a shared loader.
func fixtureDir(tb testing.TB) string {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatalf("testutil: cannot resolve own source path")
	}
	return filepath.Join(filepath.Dir(file), "testdata")
}

func readJSON(tb testing.TB, name string) []byte {
	tb.Helper()
	path := filepath.Join(fixtureDir(tb), name)
	b, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("testutil: read fixture %s: %v", name, err)
	}
	return b
}

// LoadPod unmarshals testdata/<name>.json into a typed *corev1.Pod.
func LoadPod(tb testing.TB, name string) *corev1.Pod {
	tb.Helper()
	var p corev1.Pod
	if err := json.Unmarshal(readJSON(tb, name+".json"), &p); err != nil {
		tb.Fatalf("testutil: unmarshal Pod %s: %v", name, err)
	}
	return &p
}

// LoadNode unmarshals testdata/<name>.json into a typed *corev1.Node.
func LoadNode(tb testing.TB, name string) *corev1.Node {
	tb.Helper()
	var n corev1.Node
	if err := json.Unmarshal(readJSON(tb, name+".json"), &n); err != nil {
		tb.Fatalf("testutil: unmarshal Node %s: %v", name, err)
	}
	return &n
}

// LoadUnstructured reads testdata/<name>.json as a generic unstructured
// object. Use for renderer / formatter tests that don't care about typed
// shapes, or for resources we don't have a Load* helper for yet.
func LoadUnstructured(tb testing.TB, name string) *unstructured.Unstructured {
	tb.Helper()
	u := &unstructured.Unstructured{}
	if err := u.UnmarshalJSON(readJSON(tb, name+".json")); err != nil {
		tb.Fatalf("testutil: unmarshal Unstructured %s: %v", name, err)
	}
	return u
}
