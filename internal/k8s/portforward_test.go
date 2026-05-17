package k8s

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePortPair(t *testing.T) {
	cases := []struct {
		in            string
		local, remote int
		wantErr       bool
	}{
		{"8080:80", 8080, 80, false},
		{"8080", 8080, 8080, false}, // single = same on both sides
		{"  443:8443  ", 443, 8443, false},
		{"0:80", 0, 0, true}, // 0 is reserved
		{"65535:1", 65535, 1, false},
		{"65536:1", 0, 0, true},
		{"abc:80", 0, 0, true},
		{"80:", 0, 0, true},
		{":80", 0, 0, true},
		{"", 0, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			l, r, err := ParsePortPair(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.local, l)
			assert.Equal(t, tc.remote, r)
		})
	}
}

func TestPickFreeLocalPort(t *testing.T) {
	p, err := PickFreeLocalPort()
	require.NoError(t, err)
	assert.Positive(t, p)
	assert.LessOrEqual(t, p, 65535)
}

func TestForwardManager_Count_StartsEmpty(t *testing.T) {
	fm := NewForwardManager()
	assert.Equal(t, 0, fm.Count())
	assert.Empty(t, fm.List())
}

func TestForwardManager_Stop_UnknownID(t *testing.T) {
	fm := NewForwardManager()
	err := fm.Stop("nope")
	require.Error(t, err)
}
