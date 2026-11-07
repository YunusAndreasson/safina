package investigation

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestActionUnion(t *testing.T) {
	zero, negative := int32(0), int32(-1)
	for _, action := range []Action{{Kind: "scale", Replicas: &zero}, {Kind: "image", Container: "api", Image: "registry/api:v2"}, {Kind: "restart"}, {Kind: "rollback", Revision: 2}} {
		require.NoError(t, action.Validate())
	}
	for _, action := range []Action{{Kind: "scale"}, {Kind: "scale", Replicas: &negative}, {Kind: "restart", Image: "injected"}, {Kind: "image", Container: "api"}, {Kind: "rollback", Revision: -1}, {Kind: "shell"}, {Kind: "scale", Replicas: &zero, Revision: 1}} {
		require.ErrorIs(t, action.Validate(), ErrInvalid)
	}
}

func TestImageActionRejectsInvisibleParameters(t *testing.T) {
	for _, hidden := range []string{"\x00", "\x1b[31m", "\x7f", "\u202e", "\u200b", "\u00a0", "\n", "\t"} {
		t.Run(hidden, func(t *testing.T) {
			require.ErrorIs(t, (Action{Kind: "image", Container: "web", Image: "nginx:" + hidden + "stable"}).Validate(), ErrInvalid)
			require.ErrorIs(t, (Action{Kind: "image", Container: "web" + hidden, Image: "nginx:stable"}).Validate(), ErrInvalid)
		})
	}
}
