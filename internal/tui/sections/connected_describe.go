package sections

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// LoadGenericDescribe uses the explicit API identity carried by a resource
// link. Secrets retain the existing base64 Describe behavior; decoding stays
// an explicit reveal action.
func LoadGenericDescribe(ctx context.Context, mgr *k8s.Manager, cluster string, ref NodeRef) tea.Cmd {
	if ref.SubKind == "Secret" {
		return LoadDescribe(ctx, mgr, cluster, ref.Namespace, "Secret", ref.Name)
	}
	return recordCommand(ctx, mgr, mgr.HistoryTarget(cluster, ref.Namespace, ref.SubKind, ref.Name), "describe", nil, func(callCtx context.Context) tea.Cmd {
		return func() tea.Msg {
			client, ok := mgr.GetClient(cluster)
			if !ok {
				return DescribeLoadedMsg{Err: errContextNotFound}
			}
			gvr := ref.GVR
			if gvr.Version == "" || gvr.Resource == "" || client.Dynamic == nil {
				return DescribeLoadedMsg{Err: fmt.Errorf("resource API unavailable: %s", ref.GVR)}
			}
			obj, err := client.Dynamic.Resource(gvr).Namespace(ref.Namespace).Get(callCtx, ref.Name, metav1.GetOptions{})
			if err != nil {
				return DescribeLoadedMsg{Err: err}
			}
			obj.SetManagedFields(nil)
			data, err := yaml.Marshal(obj.Object)
			return DescribeLoadedMsg{YAML: string(data), Err: err}
		}
	})
}
