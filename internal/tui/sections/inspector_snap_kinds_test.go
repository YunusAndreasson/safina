package sections

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"

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

// sectionByTitle returns the first section with a matching title.
func sectionByTitle(s *inspectorSnapshot, title string) (inspectorSection, bool) {
	for _, sec := range s.sections {
		if sec.title == title {
			return sec, true
		}
	}
	return inspectorSection{}, false
}

func TestInspectServiceSnap_OmitsRowColumns(t *testing.T) {
	// Service rows already carry name, type, ports, clusterIP, age.
	// The inspector must not restate any of them.
	svc := k8s.ServiceInfo{Name: "api", Namespace: "shop", Type: "ClusterIP", Ports: "80/TCP,443/TCP", ClusterIP: "10.0.0.1"}
	det := &inspectorDetail{service: &k8s.ServiceDetail{
		Selector:           "app=api",
		EndpointCount:      3,
		ReadyEndpointCount: 3,
	}}

	snap := inspectServiceSnap(svc, det)
	require.NotNil(t, snap)
	_, ok := rowByLabel(snap, "ports")
	assert.False(t, ok, "service inspector must not restate the ports column")
	_, ok = rowByLabel(snap, "clusterIP")
	assert.False(t, ok, "service inspector must not restate the cluster-IP column")
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

func TestInspectServiceSnap_ExtPolicyDefaultSuppressed(t *testing.T) {
	// "Cluster" is the default and shouldn't earn a row; "Local" is the
	// noteworthy setting.
	svc := k8s.ServiceInfo{Name: "svc", Type: "LoadBalancer"}
	det := &inspectorDetail{service: &k8s.ServiceDetail{ExternalTrafficPolicy: "Cluster"}}
	snap := inspectServiceSnap(svc, det)
	_, ok := rowByLabel(snap, "extPolicy")
	assert.False(t, ok, "default Cluster traffic policy should not produce a row")

	det.service.ExternalTrafficPolicy = "Local"
	snap = inspectServiceSnap(svc, det)
	got, ok := rowByLabel(snap, "extPolicy")
	require.True(t, ok)
	assert.Equal(t, "Local", got.value)
}

func TestInspectIngressSnap_OmitsHostsRestatesTLSAndBackends(t *testing.T) {
	t.Run("backends listed without restating hosts column", func(t *testing.T) {
		ing := k8s.IngressInfo{Name: "web", Hosts: "foo.example.com,bar.example.com", Class: "nginx"}
		det := &inspectorDetail{ingress: &k8s.IngressDetail{
			Rules: 2,
			Backends: []k8s.IngressBackend{
				{Host: "foo.example.com", Path: "/", Service: "foo-svc", Port: networkingv1.ServiceBackendPort{Number: 80}},
				{Host: "bar.example.com", Path: "/api", Service: "bar-svc", Port: networkingv1.ServiceBackendPort{Number: 8080}},
			},
		}}
		snap := inspectIngressSnap(ing, det)
		_, hasHosts := rowByLabel(snap, "hosts")
		assert.False(t, hasHosts, "ingress inspector must not restate the hosts column")

		sec, ok := sectionByTitle(snap, "backends")
		require.True(t, ok, "expected a backends section")
		require.Len(t, sec.rows, 2)
		assert.Equal(t, "foo.example.com/", sec.rows[0].label)
		assert.Equal(t, "foo-svc:80", sec.rows[0].value)
	})
	t.Run("no TLS surfaces (none)", func(t *testing.T) {
		ing := k8s.IngressInfo{Name: "web", Hosts: "foo.example.com", Class: "nginx"}
		det := &inspectorDetail{ingress: &k8s.IngressDetail{Rules: 1}}
		snap := inspectIngressSnap(ing, det)
		tls, ok := rowByLabel(snap, "tls")
		require.True(t, ok)
		assert.Equal(t, "(none)", tls.value)
	})
	t.Run("with TLS hosts", func(t *testing.T) {
		ing := k8s.IngressInfo{Name: "web", Hosts: "foo.example.com", Class: "nginx"}
		det := &inspectorDetail{ingress: &k8s.IngressDetail{Rules: 1, TLSHosts: []string{"foo.example.com"}}}
		snap := inspectIngressSnap(ing, det)
		tlsHosts, ok := rowByLabel(snap, "tls hosts")
		require.True(t, ok)
		assert.Equal(t, "foo.example.com", tlsHosts.value)
		assert.Equal(t, "good", tlsHosts.emphasis)
	})
	t.Run("backends truncated past the cap", func(t *testing.T) {
		ing := k8s.IngressInfo{Name: "wildcard"}
		backends := make([]k8s.IngressBackend, 0, 8)
		for range 8 {
			backends = append(backends, k8s.IngressBackend{Host: "h", Service: "svc", Port: networkingv1.ServiceBackendPort{Number: 80}})
		}
		det := &inspectorDetail{ingress: &k8s.IngressDetail{Backends: backends}}
		snap := inspectIngressSnap(ing, det)
		sec, ok := sectionByTitle(snap, "backends")
		require.True(t, ok)
		more, ok := rowByLabel(snap, "more")
		require.True(t, ok, "expected the truncation row")
		assert.Equal(t, "+3", more.value)
		assert.Len(t, sec.rows, 6, "5 backends + 1 'more' row")
	})
}

func TestInspectPVCSnap_OmitsRestatedRows(t *testing.T) {
	// Row already shows status / capacity / class / volume; the inspector
	// must not echo any of them.
	t.Run("bound with detail", func(t *testing.T) {
		p := k8s.PVCInfo{
			Name: "data-0", Namespace: "shop", Status: "Bound",
			Capacity: new(resource.MustParse("10Gi")), VolumeName: "pvc-abc-123", StorageClass: "fast",
		}
		det := &inspectorDetail{pvc: &k8s.PVCDetail{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}}}
		snap := inspectPVCSnap(p, det)
		for _, label := range []string{"volume", "capacity", "storageClass", "status"} {
			_, ok := rowByLabel(snap, label)
			assert.Falsef(t, ok, "PVC inspector must not restate the %q column", label)
		}
		got, ok := rowByLabel(snap, "accessModes")
		require.True(t, ok)
		assert.Equal(t, "RWO", got.value)
		assert.Equal(t, "Bound", snap.statusText)
		assert.Equal(t, "good", snap.statusKind)
	})
	t.Run("pending surfaces reason", func(t *testing.T) {
		p := k8s.PVCInfo{Name: "data-pending", Namespace: "shop", Status: "Pending", StorageClass: "fast"}
		det := &inspectorDetail{pvc: &k8s.PVCDetail{
			AccessModes:   []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			PendingReason: "waiting for first consumer",
		}}
		snap := inspectPVCSnap(p, det)
		got, ok := rowByLabel(snap, "pending")
		require.True(t, ok)
		assert.Equal(t, "waiting for first consumer", got.value)
		assert.Equal(t, "warn", got.emphasis)
		assert.Equal(t, "warn", snap.statusKind)
	})
	t.Run("block volume mode surfaces", func(t *testing.T) {
		p := k8s.PVCInfo{Name: "block-claim", Namespace: "shop", Status: "Bound"}
		det := &inspectorDetail{pvc: &k8s.PVCDetail{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			VolumeMode:  "Block",
		}}
		snap := inspectPVCSnap(p, det)
		got, ok := rowByLabel(snap, "volumeMode")
		require.True(t, ok)
		assert.Equal(t, "Block", got.value)
	})
	t.Run("mounted-by lists pods", func(t *testing.T) {
		p := k8s.PVCInfo{Name: "data-0", Status: "Bound"}
		det := &inspectorDetail{pvc: &k8s.PVCDetail{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			MountedBy:   []string{"web-0", "web-1"},
		}}
		snap := inspectPVCSnap(p, det)
		sec, ok := sectionByTitle(snap, "mounted by")
		require.True(t, ok)
		require.Len(t, sec.rows, 2)
		assert.Equal(t, "web-0", sec.rows[0].value)
	})
}

func TestInspectPVSnap_OmitsRestatedRows(t *testing.T) {
	t.Run("bound CSI volume", func(t *testing.T) {
		p := k8s.PVInfo{
			Name: "pv-001", Capacity: new(resource.MustParse("10Gi")), Status: "Bound",
			Claim: "shop/data-0", StorageClass: "fast",
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, Reclaim: "Retain",
		}
		det := &inspectorDetail{pv: &k8s.PVDetail{
			CSIDriver:    "ebs.csi.aws.com",
			VolumeHandle: "vol-0abc",
			NodeAffinity: "topology.kubernetes.io/zone=us-east-1a",
		}}
		snap := inspectPVSnap(p, det)
		for _, label := range []string{"capacity", "accessModes", "storageClass", "status", "claim"} {
			_, ok := rowByLabel(snap, label)
			assert.Falsef(t, ok, "PV inspector must not restate the %q column", label)
		}
		rec, ok := rowByLabel(snap, "reclaim")
		require.True(t, ok)
		assert.Equal(t, "Retain", rec.value)
		assert.Equal(t, "good", rec.emphasis)

		got, ok := rowByLabel(snap, "csi driver")
		require.True(t, ok)
		assert.Equal(t, "ebs.csi.aws.com", got.value)

		got, ok = rowByLabel(snap, "topology")
		require.True(t, ok)
		assert.Contains(t, got.value, "us-east-1a")

		assert.Equal(t, "Bound", snap.statusText)
		assert.Equal(t, "good", snap.statusKind)
	})
	t.Run("delete reclaim is loud", func(t *testing.T) {
		p := k8s.PVInfo{Name: "pv-002", Capacity: new(resource.MustParse("5Gi")), Status: "Available", Reclaim: "Delete"}
		snap := inspectPVSnap(p, nil)
		rec, ok := rowByLabel(snap, "reclaim")
		require.True(t, ok)
		assert.Equal(t, "bad", rec.emphasis, "Delete reclaim should render as bad")
		assert.Equal(t, "good", snap.statusKind, "Available is a good steady state")
	})
}

func TestInspectConfigMapSnap_PreviewsKeys(t *testing.T) {
	t.Run("no keys keeps subtitle only", func(t *testing.T) {
		snap := inspectConfigMapSnap(k8s.ConfigMapInfo{Name: "cfg"})
		require.Len(t, snap.shortcuts, 1)
		assert.Equal(t, "d", snap.shortcuts[0].Key)
		assert.Empty(t, snap.sections)
	})
	t.Run("keys render as a single dim row", func(t *testing.T) {
		cm := k8s.ConfigMapInfo{Name: "cfg", Keys: 3, KeyNames: []string{"db.host", "db.port", "feature.flags"}}
		snap := inspectConfigMapSnap(cm)
		got, ok := rowByLabel(snap, "keys")
		require.True(t, ok)
		assert.Contains(t, got.value, "db.host")
		assert.Contains(t, got.value, "feature.flags")
	})
	t.Run("more than five truncate with +N", func(t *testing.T) {
		names := []string{"a", "b", "c", "d", "e", "f", "g"}
		cm := k8s.ConfigMapInfo{Name: "cfg", Keys: len(names), KeyNames: names}
		snap := inspectConfigMapSnap(cm)
		got, ok := rowByLabel(snap, "keys")
		require.True(t, ok)
		assert.Contains(t, got.value, "(+2 more)")
	})
}

func TestInspectSecretSnap_PreviewsKeys(t *testing.T) {
	t.Run("no keys keeps subtitle only", func(t *testing.T) {
		snap := inspectSecretSnap(k8s.SecretInfo{Name: "creds", Type: "Opaque"})
		require.Len(t, snap.shortcuts, 1)
		assert.Equal(t, "v", snap.shortcuts[0].Key)
		assert.Empty(t, snap.sections)
	})
	t.Run("TLS key names render", func(t *testing.T) {
		sec := k8s.SecretInfo{
			Name: "tls-cert", Type: "kubernetes.io/tls", Keys: 2,
			KeyNames: []string{"tls.crt", "tls.key"},
		}
		snap := inspectSecretSnap(sec)
		got, ok := rowByLabel(snap, "keys")
		require.True(t, ok)
		assert.Contains(t, got.value, "tls.crt")
		assert.Contains(t, got.value, "tls.key")
	})
}

func TestInspectContainerSnap_OmitsImageAndAddsDetail(t *testing.T) {
	c := k8s.ContainerInfo{
		PodName:    "web-0",
		Name:       "app",
		Image:      "ghcr.io/co/app:v1.2.3",
		Kind:       "regular",
		State:      "Running",
		Ready:      true,
		Ports:      []string{"http:8080/TCP"},
		CPURequest: 100, CPULimit: 500,
		MemRequest: 64 * 1024 * 1024, MemLimit: 128 * 1024 * 1024,
		Liveness:  "http :8080/healthz",
		Readiness: "http :8080/ready",
		Mounts:    3,
		EnvCount:  2,
	}
	snap := inspectContainerSnap(c)
	_, ok := rowByLabel(snap, "image")
	assert.False(t, ok, "container inspector must not restate the row's image column")

	got, ok := rowByLabel(snap, "ports")
	require.True(t, ok)
	assert.Equal(t, "http:8080/TCP", got.value)

	_, ok = sectionByTitle(snap, "resources (configured)")
	assert.True(t, ok, "expected configured resources section")

	probes, ok := sectionByTitle(snap, "probes")
	require.True(t, ok)
	assert.Len(t, probes.rows, 2, "liveness + readiness")
}
