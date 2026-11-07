package k8s

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"
	"sigs.k8s.io/yaml"
)

// Exercise the same safety contract through every public edit dispatcher branch.
// Keep fixtures explicit: adding an editable kind should add a case here.
func TestEditContract(t *testing.T) {
	cases := []struct{ kind, version, fields string }{
		{EditKindDeployment, "apps/v1", "spec: {replicas: 3}"},
		{EditKindStatefulSet, "apps/v1", "spec: {replicas: 3, serviceName: db}"},
		{EditKindDaemonSet, "apps/v1", "spec: {minReadySeconds: 10}"},
		{EditKindConfigMap, "v1", "data:\n  config: |\n    original\n    ---\n    embedded document separator"},
		{EditKindSecret, "v1", "type: Opaque\ndata: {text: aGVsbG8=, binary: //4AgA==}"},
		{EditKindService, "v1", "spec: {clusterIP: 10.0.0.1, ports: [{port: 80}]}"},
		{EditKindIngress, "networking.k8s.io/v1", "spec: {ingressClassName: nginx}"},
		{EditKindPVC, "v1", "spec: {volumeName: disk, accessModes: [ReadWriteOnce]}"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			original := fmt.Sprintf("# Editor preamble\n---\n# Edited resource\napiVersion: %s\nkind: %s\nmetadata:\n  name: sample\n  namespace: ns\n  resourceVersion: \"17\"\n  labels: {keep: me}\n%s\n", tc.version, tc.kind, tc.fields)
			newObject := func(t *testing.T) runtime.Object {
				t.Helper()
				gv, err := schema.ParseGroupVersion(tc.version)
				require.NoError(t, err)
				obj, err := scheme.Scheme.New(gv.WithKind(tc.kind))
				require.NoError(t, err)
				require.NoError(t, yaml.UnmarshalStrict([]byte(original), obj))
				return obj
			}
			t.Run("round trip preserves fields", func(t *testing.T) {
				cs := fake.NewSimpleClientset(newObject(t))
				document, err := BuildEditableYAML(t.Context(), cs, tc.kind, "ns", "sample")
				require.NoError(t, err)
				// Make a real edit while retaining all other fields and concurrency metadata.
				document = strings.Replace(document, "keep: me", "keep: changed", 1)
				document = "# Editor preamble\n---\n" + document
				cs.ClearActions()
				require.NoError(t, ApplyEditedYAML(t.Context(), cs, tc.kind, "ns", "sample", []byte(document)))
				actions := cs.Actions()
				require.Len(t, actions, 1)
				update, ok := actions[0].(ktesting.UpdateAction)
				require.True(t, ok, "edit must issue one Update")
				expected := newObject(t)
				require.NoError(t, yaml.UnmarshalStrict([]byte(strings.Replace(original, "keep: me", "keep: changed", 1)), expected))
				got := update.GetObject()
				// The API server merges stringData into data; the fake tracker does not.
				// Compare effective Secret bytes explicitly, including non-UTF8 values.
				if tc.kind == EditKindSecret {
					secret, ok := got.(*corev1.Secret)
					require.True(t, ok)
					if secret.Data == nil {
						secret.Data = make(map[string][]byte)
					}
					for key, value := range secret.StringData {
						secret.Data[key] = []byte(value)
					}
					secret.StringData = nil
				}
				assert.Equal(t, expected, got)
			})
			invalid := map[string]string{
				"renamed":                     strings.Replace(original, "name: sample", "name: other", 1),
				"moved namespace":             strings.Replace(original, "namespace: ns", "namespace: other", 1),
				"unknown field":               original + "misspelledField: value\n",
				"duplicate field":             original + "metadata: {name: sample, namespace: ns}\n",
				"second document":             original + "---\n" + original,
				"malformed trailing document": original + "---\ninvalid: [\n",
				"explicit document end":       original + "...\n---\n" + original,
				"missing resource version":    strings.Replace(original, "  resourceVersion: \"17\"\n", "", 1),
				"empty resource version":      strings.Replace(original, "resourceVersion: \"17\"", "resourceVersion: \"\"", 1),
			}
			for name, document := range invalid {
				t.Run(name, func(t *testing.T) {
					cs := fake.NewSimpleClientset(newObject(t))
					err := ApplyEditedYAML(t.Context(), cs, tc.kind, "ns", "sample", []byte(document))
					assert.Empty(t, cs.Actions(), "unsafe edits must not reach Kubernetes")
					require.Error(t, err)
				})
			}
		})
	}
}

// Use the real REST client for the wire contract. The fake tracker neither
// enforces resourceVersion conflicts nor models HTTP error decoding.
func TestEditConflictPreservesVersionAndDoesNotRetry(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "/api/v1/namespaces/ns/configmaps/sample", r.URL.Path)
		var obj struct {
			Metadata metav1.ObjectMeta `json:"metadata"`
			Data     map[string]string `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&obj); !assert.NoError(t, err) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		assert.Equal(t, "17", obj.Metadata.ResourceVersion)
		assert.Equal(t, "new", obj.Data["key"])
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		err := json.NewEncoder(w).Encode(&metav1.Status{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
			Status:   metav1.StatusFailure, Reason: metav1.StatusReasonConflict, Code: http.StatusConflict,
			Message: "object has been modified",
		})
		assert.NoError(t, err)
	}))
	defer server.Close()
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL, ContentConfig: rest.ContentConfig{ContentType: "application/json"}})
	require.NoError(t, err)
	err = ApplyEditedYAML(t.Context(), cs, EditKindConfigMap, "ns", "sample", []byte("metadata: {name: sample, namespace: ns, resourceVersion: '17'}\ndata: {key: new}\n"))
	require.True(t, apierrors.IsConflict(err), "want conflict, got %v", err)
	assert.Equal(t, int32(1), requests.Load(), "a stale edit must not overwrite a concurrent change")
}
