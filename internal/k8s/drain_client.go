package k8s

import (
	"context"
	"errors"

	"github.com/YunusAndreasson/safina/internal/history"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	typedpolicy "k8s.io/client-go/kubernetes/typed/policy/v1"
)

// drainClient retains Safina's eviction-only policy and records each API attempt,
// including retries hidden inside kubectl's helper.
type drainClient struct {
	kubernetes.Interface
	uids map[types.NamespacedName]types.UID
}

func (c drainClient) Discovery() discovery.DiscoveryInterfaces {
	return evictionDiscovery{DiscoveryInterfaces: c.Interface.Discovery()}
}

func (c drainClient) PolicyV1() typedpolicy.PolicyV1Interface {
	return recordedPolicy{PolicyV1Interface: c.Interface.PolicyV1(), uids: c.uids}
}

type evictionDiscovery struct{ discovery.DiscoveryInterfaces }

func (d evictionDiscovery) ServerResourcesForGroupVersion(gv string) (*metav1.APIResourceList, error) {
	resources, err := d.DiscoveryInterfaces.ServerResourcesForGroupVersion(gv)
	if err != nil {
		return nil, err
	}
	if gv == "v1" {
		if resources != nil {
			for _, r := range resources.APIResources {
				if r.Name == "pods/eviction" && r.Kind == "Eviction" && r.Group == "policy" && r.Version == "v1" {
					return &metav1.APIResourceList{GroupVersion: resources.GroupVersion, APIResources: []metav1.APIResource{r}}, nil
				}
			}
		}
		return nil, errors.New("drain requires policy/v1 eviction support; refusing direct pod deletion")
	}
	return resources, nil
}

type recordedPolicy struct {
	typedpolicy.PolicyV1Interface
	uids map[types.NamespacedName]types.UID
}

func (p recordedPolicy) Evictions(namespace string) typedpolicy.EvictionInterface {
	return recordedEvictions{EvictionInterface: p.PolicyV1Interface.Evictions(namespace), uids: p.uids}
}

type recordedEvictions struct {
	typedpolicy.EvictionInterface
	uids map[types.NamespacedName]types.UID
}

func (e recordedEvictions) Evict(ctx context.Context, eviction *policyv1.Eviction) error {
	uid := e.uids[types.NamespacedName{Namespace: eviction.Namespace, Name: eviction.Name}]
	if uid != "" {
		eviction = eviction.DeepCopy()
		if eviction.DeleteOptions == nil {
			eviction.DeleteOptions = &metav1.DeleteOptions{}
		}
		eviction.DeleteOptions.Preconditions = &metav1.Preconditions{UID: &uid}
	}
	var op *history.Operation
	if parent := history.FromContext(ctx); parent != nil {
		target := parent.Target
		target.Kind, target.Name, target.Namespace = "Pod", eviction.Name, eviction.Namespace
		target.UID = ""
		if eviction.DeleteOptions != nil && eviction.DeleteOptions.Preconditions != nil && eviction.DeleteOptions.Preconditions.UID != nil {
			target.UID = *eviction.DeleteOptions.Preconditions.UID
		}
		op = parent.Store.Begin(target, "evict", map[string]string{"drainOperation": parent.ID})
	}
	err := e.EvictionInterface.Evict(ctx, eviction)
	if op != nil {
		op.Finish(err, "accepted")
	}
	return err
}
