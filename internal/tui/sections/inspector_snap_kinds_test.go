package sections

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yunus/safina/internal/k8s"
)

// rowByLabel returns the first row matching label across all sections, or
// false if the snapshot has no such row. Tests prefer this over
// collectRowValues when they need to assert on emphasis as well as value.
func rowByLabel(s *inspectorSnapshot, label string) (inspectorRow, bool) {
	for _, sec := range s.sections {
		for _, r := range sec.rows {
			if r.label == label {
				return r, true
			}
		}
	}
	return inspectorRow{}, false
}

func TestInspectServiceSnap_ListsPorts(t *testing.T) {
	svc := k8s.ServiceInfo{Name: "api", Namespace: "shop", Type: "ClusterIP", Ports: "80/TCP,443/TCP"}
	det := &inspectorDetail{service: &k8s.ServiceDetail{
		Selector:           "app=api",
		EndpointCount:      3,
		ReadyEndpointCount: 3,
	}}

	snap := inspectServiceSnap(svc, det)

	require.NotNil(t, snap)
	got, ok := rowByLabel(snap, "ports")
	require.True(t, ok, "service snapshot must surface a ports row")
	assert.Equal(t, "80/TCP,443/TCP", got.value)
}

func TestInspectServiceSnap_HeadlessAndExternalName(t *testing.T) {
	t.Run("headless", func(t *testing.T) {
		svc := k8s.ServiceInfo{Name: "h", Type: "ClusterIP", ClusterIP: "None"}
		det := &inspectorDetail{service: &k8s.ServiceDetail{HeadlessClusterIP: true}}

		snap := inspectServiceSnap(svc, det)
		_, ok := rowByLabel(snap, "mode")
		assert.True(t, ok, "headless services should keep the mode row (regression guard)")
	})
	t.Run("externalName", func(t *testing.T) {
		svc := k8s.ServiceInfo{Name: "e", Type: "ExternalName"}
		det := &inspectorDetail{service: &k8s.ServiceDetail{ExternalName: "db.example.com"}}

		snap := inspectServiceSnap(svc, det)
		got, ok := rowByLabel(snap, "external")
		require.True(t, ok)
		assert.Equal(t, "db.example.com", got.value)
	})
}

func TestInspectServiceSnap_EndpointEmphasis(t *testing.T) {
	cases := map[string]struct {
		ready, total int
		want         string
	}{
		"none":      {0, 0, "bad"},
		"partial":   {1, 3, "warn"},
		"all-ready": {3, 3, "good"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc := k8s.ServiceInfo{Name: "svc", Type: "ClusterIP"}
			det := &inspectorDetail{service: &k8s.ServiceDetail{
				EndpointCount: tc.total, ReadyEndpointCount: tc.ready,
			}}
			snap := inspectServiceSnap(svc, det)
			got, ok := rowByLabel(snap, "endpoints")
			require.True(t, ok)
			assert.Equal(t, tc.want, got.emphasis)
		})
	}
}

func TestInspectIngressSnap_ListsHostsAndTLS(t *testing.T) {
	t.Run("hosts without TLS", func(t *testing.T) {
		ing := k8s.IngressInfo{Name: "web", Hosts: "foo.example.com,bar.example.com", Class: "nginx"}
		det := &inspectorDetail{ingress: &k8s.IngressDetail{Rules: 2}}

		snap := inspectIngressSnap(ing, det)
		host, ok := rowByLabel(snap, "hosts")
		require.True(t, ok, "ingress snapshot must surface a hosts row")
		assert.Equal(t, "foo.example.com,bar.example.com", host.value)
		tls, ok := rowByLabel(snap, "tls")
		require.True(t, ok)
		assert.Equal(t, "(none)", tls.value)
	})
	t.Run("hosts with TLS", func(t *testing.T) {
		ing := k8s.IngressInfo{Name: "web", Hosts: "foo.example.com", Class: "nginx"}
		det := &inspectorDetail{ingress: &k8s.IngressDetail{Rules: 1, TLSHosts: []string{"foo.example.com"}}}

		snap := inspectIngressSnap(ing, det)
		_, hasHosts := rowByLabel(snap, "hosts")
		assert.True(t, hasHosts)
		tlsHosts, ok := rowByLabel(snap, "tls hosts")
		require.True(t, ok)
		assert.Equal(t, "foo.example.com", tlsHosts.value)
		assert.Equal(t, "good", tlsHosts.emphasis)
	})
}

func TestInspectPVCSnap_PromotedToSection(t *testing.T) {
	t.Run("bound", func(t *testing.T) {
		p := k8s.PVCInfo{
			Name: "data-0", Namespace: "shop", Status: "Bound",
			Capacity: "10Gi", VolumeName: "pvc-abc-123", StorageClass: "fast",
		}
		snap := inspectPVCSnap(p)
		require.NotEmpty(t, snap.sections, "bound PVC must have at least one section")

		vol, ok := rowByLabel(snap, "volume")
		require.True(t, ok)
		assert.Equal(t, "pvc-abc-123", vol.value)

		for _, label := range []string{"capacity", "storageClass", "status"} {
			_, ok := rowByLabel(snap, label)
			assert.True(t, ok, "expected %q row", label)
		}
		status, _ := rowByLabel(snap, "status")
		assert.Equal(t, "good", status.emphasis, "Bound should render as good")
	})
	t.Run("pending omits volume", func(t *testing.T) {
		p := k8s.PVCInfo{Name: "data-pending", Namespace: "shop", Status: "Pending", StorageClass: "fast"}
		snap := inspectPVCSnap(p)
		_, hasVolume := rowByLabel(snap, "volume")
		assert.False(t, hasVolume, "Pending PVCs (no VolumeName) should omit the volume row")
		status, ok := rowByLabel(snap, "status")
		require.True(t, ok)
		assert.Equal(t, "warn", status.emphasis)
	})
}

func TestInspectPVSnap_PromotedToSection(t *testing.T) {
	t.Run("bound", func(t *testing.T) {
		p := k8s.PVInfo{
			Name: "pv-001", Capacity: "10Gi", Status: "Bound",
			Claim: "shop/data-0", StorageClass: "fast",
			AccessModes: "RWO", Reclaim: "Retain",
		}
		snap := inspectPVSnap(p)
		require.NotEmpty(t, snap.sections)

		claim, ok := rowByLabel(snap, "claim")
		require.True(t, ok)
		assert.Equal(t, "shop/data-0", claim.value)

		for _, label := range []string{"capacity", "accessModes", "reclaim", "storageClass", "status"} {
			_, ok := rowByLabel(snap, label)
			assert.True(t, ok, "expected %q row", label)
		}
		rec, _ := rowByLabel(snap, "reclaim")
		assert.Equal(t, "good", rec.emphasis, "Retain should render as good")
	})
	t.Run("available omits claim", func(t *testing.T) {
		p := k8s.PVInfo{Name: "pv-002", Capacity: "5Gi", Status: "Available", Reclaim: "Delete"}
		snap := inspectPVSnap(p)
		_, hasClaim := rowByLabel(snap, "claim")
		assert.False(t, hasClaim, "Available PVs (no claim) should omit the claim row")
		rec, ok := rowByLabel(snap, "reclaim")
		require.True(t, ok)
		assert.Equal(t, "bad", rec.emphasis, "Delete reclaim should render as bad")
	})
}

func TestInspectConfigMapAndSecretSnap_UnchangedSubtitle(t *testing.T) {
	cm := inspectConfigMapSnap(k8s.ConfigMapInfo{Name: "cfg", Keys: 3})
	assert.Contains(t, cm.subtitle, "press d", "ConfigMap snapshot subtitle is deliberately minimal — locks in the opinionated UX")
	assert.Empty(t, cm.sections, "ConfigMap snapshot should not emit row sections")

	sec := inspectSecretSnap(k8s.SecretInfo{Name: "creds", Type: "Opaque", Keys: 2})
	assert.Contains(t, sec.subtitle, "press v", "Secret snapshot subtitle is deliberately minimal — locks in the opinionated UX")
	assert.Empty(t, sec.sections)
}
