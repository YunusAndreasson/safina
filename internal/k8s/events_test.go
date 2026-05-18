package k8s

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

var evtSeq int

func newEvent(ns, name, kind, reason, msg string, lastSeen, eventTime, created time.Time, count int32, typ string) *corev1.Event {
	evtSeq++
	return &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:              fmt.Sprintf("%s-evt-%d", name, evtSeq),
			Namespace:         ns,
			CreationTimestamp: metav1.NewTime(created),
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      kind,
			Name:      name,
			Namespace: ns,
		},
		Reason:        reason,
		Message:       msg,
		Type:          typ,
		Count:         count,
		LastTimestamp: metav1.NewTime(lastSeen),
		EventTime:     metav1.NewMicroTime(eventTime),
	}
}

func TestListEvents_NewestFirstByLastSeen(t *testing.T) {
	now := time.Now()
	older := newEvent("default", "p1", "Pod", "Failed", "old",
		now.Add(-10*time.Minute), time.Time{}, now.Add(-15*time.Minute), 1, "Warning")
	newer := newEvent("default", "p1", "Pod", "BackOff", "new",
		now.Add(-1*time.Minute), time.Time{}, now.Add(-5*time.Minute), 3, "Warning")
	cs := fake.NewSimpleClientset(older, newer)

	got, err := ListEvents(context.Background(), cs, "default", "Pod", "p1")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "BackOff", got[0].Reason, "newest event (by LastSeen) must come first")
	assert.Equal(t, "Failed", got[1].Reason)
	// LastSeen must be the actual LastTimestamp, not the CreationTimestamp.
	assert.True(t, got[0].LastSeen.After(got[1].LastSeen))
}

func TestListEvents_FallsBackToEventTimeWhenLastTimestampZero(t *testing.T) {
	// New-style server events fill EventTime but not LastTimestamp. The
	// reader must use it for sorting and Age display — otherwise these
	// would all sort as "epoch 0" and the user sees garbled ordering.
	now := time.Now()
	created := now.Add(-30 * time.Minute)
	evt := newEvent("default", "p2", "Pod", "Pulled", "image pulled",
		time.Time{}, now.Add(-5*time.Minute), created, 1, "Normal")
	cs := fake.NewSimpleClientset(evt)

	got, err := ListEvents(context.Background(), cs, "default", "Pod", "p2")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.False(t, got[0].LastSeen.IsZero(),
		"LastSeen must fall back to EventTime when LastTimestamp is zero")
	assert.WithinDuration(t, now.Add(-5*time.Minute), got[0].LastSeen, time.Second)
}

func TestListEvents_FallsBackToCreationWhenBothEventTimesZero(t *testing.T) {
	// Last-resort fallback. Without this, very old events from clusters
	// running pre-1.20 servers (which often don't fill either field) would
	// have garbage Age.
	now := time.Now()
	created := now.Add(-1 * time.Hour)
	evt := newEvent("default", "p3", "Pod", "Created", "container created",
		time.Time{}, time.Time{}, created, 1, "Normal")
	cs := fake.NewSimpleClientset(evt)

	got, err := ListEvents(context.Background(), cs, "default", "Pod", "p3")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.WithinDuration(t, created, got[0].LastSeen, time.Second)
}

func TestListEvents_PreservesTypeReasonMessageCount(t *testing.T) {
	now := time.Now()
	evt := newEvent("ns", "thing", "Pod", "BackOff", "Back-off restarting failed container",
		now.Add(-2*time.Minute), time.Time{}, now.Add(-3*time.Minute), 12, "Warning")
	cs := fake.NewSimpleClientset(evt)

	got, err := ListEvents(context.Background(), cs, "ns", "Pod", "thing")
	require.NoError(t, err)
	require.Len(t, got, 1)
	e := got[0]
	assert.Equal(t, "Warning", e.Type)
	assert.Equal(t, "BackOff", e.Reason)
	assert.Equal(t, "Back-off restarting failed container", e.Message)
	assert.Equal(t, int32(12), e.Count, "Count must be preserved verbatim — operators read it to spot flapping")
	assert.NotEmpty(t, e.Age, "Age must be populated for the UI")
}

func TestListEvents_EmptyResultIsNotAnError(t *testing.T) {
	// Healthy resources have no events. The list call returning an empty
	// slice is the expected steady state — not an error condition.
	cs := fake.NewSimpleClientset()
	got, err := ListEvents(context.Background(), cs, "default", "Pod", "p")
	require.NoError(t, err)
	assert.NotNil(t, got, "empty events must return a non-nil slice for downstream consumers")
	assert.Empty(t, got)
}
