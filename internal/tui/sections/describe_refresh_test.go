package sections

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/keys"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
)

func TestSecretRefreshPreservesRevealMode(t *testing.T) {
	cs := fake.NewClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "secret", Namespace: "qa"},
		Data:       map[string][]byte{"token": []byte("fake-qa-token")},
	})
	c := &tuictx.Ctx{
		Theme: theme.Default(), Keys: keys.Default(), Background: context.Background(), ActiveContext: "demo",
		Manager: &k8s.Manager{Clients: map[string]*k8s.Client{"demo": {Clientset: cs}}},
	}
	for _, revealed := range []bool{false, true} {
		s := NewDescribe(c)
		s.Reset("Secret", "secret", "qa")
		if revealed {
			s.MarkRevealed()
		}
		cmd, leave := s.Update(tea.KeyPressMsg{Code: 'r', Text: "r"}, c)
		require.False(t, leave)
		require.NotNil(t, cmd)
		result, ok := cmd().(DescribeLoadedMsg)
		require.True(t, ok)
		require.NoError(t, result.Err)
		if revealed {
			require.Contains(t, result.YAML, "token: fake-qa-token")
			require.Contains(t, result.YAML, "stringData:")
		} else {
			require.NotContains(t, result.YAML, "fake-qa-token")
			require.Contains(t, result.YAML, "data:")
		}
	}
}
