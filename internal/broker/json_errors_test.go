package broker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDigestRejectsUnencodableValues(t *testing.T) {
	for _, value := range []any{make(chan int), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		sum, err := digest(value)
		require.Error(t, err)
		require.Empty(t, sum, "an encoding failure must not become a valid digest")
	}
}

func TestWriteJSONEncodingFailureReturnsServerError(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, struct {
		Time time.Time `json:"time"`
	}{Time: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)})
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.True(t, strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain"))
	require.Equal(t, "Could not encode response\n", w.Body.String())
}
