package k8s

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestRevealSecretYAML_DecodesData(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "ns"},
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"username": []byte("alice"),
			"password": []byte("hunter2"),
		},
	}
	cs := fake.NewSimpleClientset(sec)

	yaml, err := RevealSecretYAML(context.Background(), cs, "ns", "creds")
	require.NoError(t, err)
	// Decoded plaintext lives under stringData; the base64 form of "alice"
	// (YWxpY2U=) must be absent — that's the marker the raw Data field got
	// emptied out instead of also being serialized.
	assert.Contains(t, yaml, "stringData")
	assert.Contains(t, yaml, "alice")
	assert.Contains(t, yaml, "hunter2")
	assert.NotContains(t, yaml, "YWxpY2U")
	assert.NotContains(t, yaml, "managedFields")
	assert.Contains(t, yaml, "kind: Secret")
}

func TestRevealSecretYAML_NoData(t *testing.T) {
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "empty", Namespace: "ns"},
		Type:       corev1.SecretTypeOpaque,
	}
	cs := fake.NewSimpleClientset(sec)

	yaml, err := RevealSecretYAML(context.Background(), cs, "ns", "empty")
	require.NoError(t, err)
	assert.Contains(t, yaml, "kind: Secret")
	assert.NotContains(t, yaml, "stringData")
}

func TestRevealSecretYAML_NotFound(t *testing.T) {
	cs := fake.NewSimpleClientset()
	_, err := RevealSecretYAML(context.Background(), cs, "ns", "missing")
	require.Error(t, err)
}
