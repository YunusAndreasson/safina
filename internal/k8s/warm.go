package k8s

// WarmResourceCache starts a lazy cache without waiting for synchronization or
// issuing a direct read. It never activates an inactive cluster. The caller's
// normal read path still falls back to the API until this cache is ready.
func (c *Client) WarmResourceCache(kind string) {
	inf := c.Informers()
	if inf == nil {
		return
	}
	switch kind {
	case KindService:
		inf.Services()
	case KindConfigMap:
		inf.ConfigMaps()
	case KindSecret:
		inf.Secrets()
	case KindPVC:
		inf.PVCs()
	case KindIngress:
		inf.Ingresses()
	}
}
