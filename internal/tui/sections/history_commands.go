package sections

import (
	"context"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/YunusAndreasson/safina/internal/k8s"
)

func LoadDescribe(ctx context.Context, mgr *k8s.Manager, contextName, namespace, kind, name string) tea.Cmd {
	return recordCommand(ctx, mgr, mgr.HistoryTarget(contextName, namespace, kind, name), "describe", nil, func(callCtx context.Context) tea.Cmd {
		return rawLoadDescribe(callCtx, mgr, contextName, namespace, kind, name)
	})
}

func LoadSecretReveal(ctx context.Context, mgr *k8s.Manager, contextName, namespace, name string) tea.Cmd {
	return recordCommand(ctx, mgr, mgr.HistoryTarget(contextName, namespace, "Secret", name), "reveal_secret", nil, func(callCtx context.Context) tea.Cmd {
		return rawLoadSecretReveal(callCtx, mgr, contextName, namespace, name)
	})
}

func LoadEditYAML(ctx context.Context, mgr *k8s.Manager, contextName, namespace, kind, name string) tea.Cmd {
	return recordCommand(ctx, mgr, mgr.HistoryTarget(contextName, namespace, kind, name), "edit_open", nil, func(callCtx context.Context) tea.Cmd {
		return rawLoadEditYAML(callCtx, mgr, contextName, namespace, kind, name)
	})
}

func ApplyEdit(ctx context.Context, mgr *k8s.Manager, contextName, namespace, kind, name string, data []byte) tea.Cmd {
	return recordCommand(ctx, mgr, mgr.HistoryTarget(contextName, namespace, kind, name), "edit", nil, func(callCtx context.Context) tea.Cmd {
		mgr.RecordEditPaths(callCtx, mgr.HistoryTarget(contextName, namespace, kind, name), data)
		return rawApplyEdit(callCtx, mgr, contextName, namespace, kind, name, data)
	})
}

func LoadHot(ctx context.Context, mgr *k8s.Manager, contextName string) tea.Cmd {
	return recordCommand(ctx, mgr, mgr.HistoryTarget(contextName, "", "", ""), "hot_pods", nil, func(callCtx context.Context) tea.Cmd { return rawLoadHot(callCtx, mgr, contextName) })
}

func LoadEvents(ctx context.Context, mgr *k8s.Manager, contextName, namespace, kind, name string) tea.Cmd {
	return recordCommand(ctx, mgr, mgr.HistoryTarget(contextName, namespace, kind, name), "events", nil, func(callCtx context.Context) tea.Cmd {
		return rawLoadEvents(callCtx, mgr, contextName, namespace, kind, name)
	})
}

func LoadDiscovery(ctx context.Context, mgr *k8s.Manager, contextName string) tea.Cmd {
	return recordCommand(ctx, mgr, mgr.HistoryTarget(contextName, "", "", ""), "discovery", nil, func(callCtx context.Context) tea.Cmd { return rawLoadDiscovery(mgr, contextName) })
}

func DeleteResource(ctx context.Context, mgr *k8s.Manager, contextName, kind, namespace, name string) tea.Cmd {
	return recordCommand(ctx, mgr, mgr.HistoryTarget(contextName, namespace, kind, name), "delete", nil, func(callCtx context.Context) tea.Cmd {
		return rawDeleteResource(callCtx, mgr, contextName, kind, namespace, name)
	})
}

func ScaleWorkload(ctx context.Context, mgr *k8s.Manager, contextName, namespace, kind, name string, replicas int32) tea.Cmd {
	return recordCommand(ctx, mgr, mgr.HistoryTarget(contextName, namespace, kind, name), "scale", map[string]string{"replicas": strconv.Itoa(int(replicas))}, func(callCtx context.Context) tea.Cmd {
		return rawScaleWorkload(callCtx, mgr, contextName, namespace, kind, name, replicas)
	})
}

func RollbackDeployment(ctx context.Context, mgr *k8s.Manager, contextName, namespace, name string) tea.Cmd {
	return recordCommand(ctx, mgr, mgr.HistoryTarget(contextName, namespace, "Deployment", name), "rollback", nil, func(callCtx context.Context) tea.Cmd {
		return rawRollbackDeployment(callCtx, mgr, contextName, namespace, name)
	})
}

func RestartPod(ctx context.Context, mgr *k8s.Manager, contextName, namespace, name string) tea.Cmd {
	return recordCommand(ctx, mgr, mgr.HistoryTarget(contextName, namespace, "Pod", name), "restart", nil, func(callCtx context.Context) tea.Cmd {
		return rawRestartPod(callCtx, mgr, contextName, namespace, name)
	})
}

func RestartApp(ctx context.Context, mgr *k8s.Manager, contextName, namespace, kind, name string) tea.Cmd {
	return recordCommand(ctx, mgr, mgr.HistoryTarget(contextName, namespace, kind, name), "restart", nil, func(callCtx context.Context) tea.Cmd {
		return rawRestartApp(callCtx, mgr, contextName, namespace, kind, name)
	})
}

func OpenShell(ctx context.Context, mgr *k8s.Manager, contextName, namespace, pod, container string) tea.Cmd {
	return recordCommand(ctx, mgr, mgr.HistoryTarget(contextName, namespace, "Pod", pod), "shell", map[string]string{"container": container}, func(callCtx context.Context) tea.Cmd {
		return rawOpenShell(callCtx, mgr, contextName, namespace, pod, container)
	})
}

func AttachDebug(ctx context.Context, mgr *k8s.Manager, contextName, namespace, pod, targetContainer, image string) tea.Cmd {
	return recordCommand(ctx, mgr, mgr.HistoryTarget(contextName, namespace, "Pod", pod), "debug_container", map[string]string{"container": targetContainer, "image": image}, func(callCtx context.Context) tea.Cmd {
		return rawAttachDebug(callCtx, mgr, contextName, namespace, pod, targetContainer, image)
	})
}
