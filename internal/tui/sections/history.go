package sections

import (
	"context"
	"encoding/json"

	tea "charm.land/bubbletea/v2"
	"github.com/yunus/safina/internal/history"
	"github.com/yunus/safina/internal/k8s"
)

func recordCommand(ctx context.Context, mgr *k8s.Manager, target history.Target, action string, params map[string]string, factory func(context.Context) tea.Cmd) tea.Cmd {
	if mgr.History == nil {
		return factory(ctx)
	}
	return func() tea.Msg {
		if params != nil {
			target.Container = params["container"]
		}
		op := mgr.History.Begin(target, action, params)
		mutation := action == "scale" || action == "restart" || action == "rollback" || action == "edit" || action == "delete"
		client, ok := mgr.GetClient(target.Context)
		if mutation && ok && mgr.History.EvidenceEnabled() {
			c, cancel := k8s.SnapshotContext(ctx)
			t, fields := client.HistorySnapshot(c, target)
			cancel()
			op.Target = t
			op.Record("before", "", fields)
		}
		msg := factory(history.WithOperation(ctx, op))()
		var err error
		outcome := "completed"
		recordedResult := false
		switch v := msg.(type) {
		case ActionDoneMsg:
			err = v.Err
			outcome = "accepted"
			op.Finish(err, outcome)
			recordedResult = true
			v.Operation = op
			if err == nil && mutation && ok && (target.Kind == "Deployment" || target.Kind == "StatefulSet" || target.Kind == "DaemonSet") && action != "delete" {
				c, cancel := k8s.SnapshotContext(ctx)
				t, fields := client.HistorySnapshot(c, target)
				cancel()
				if op.Target.UID == "" {
					op.Target = t
				}
				v.Generation = op.Generation
				if v.Generation == "" && op.ResourceVersion != "" && op.ResourceVersion == fields["resourceVersion"] {
					v.Generation = fields["generation"]
				}
				if v.Generation == "" {
					op.Record("monitoring_unverified", "accepted generation unavailable; readiness cannot be attributed", nil)
				}
				if mgr.History.EvidenceEnabled() {
					op.Record("after", "", fields)
				}
				v.Target = &WorkloadTarget{Context: target.Context, Namespace: target.Namespace, Kind: target.Kind, Name: target.Name}
			}
			msg = v
		case DescribeLoadedMsg:
			err = v.Err
		case EditYAMLLoadedMsg:
			err = v.Err
		case HotLoadedMsg:
			err = v.Err
		case DiscoveryLoadedMsg:
			err = v.Err
		case EventsLoadedMsg:
			err = v.Err
			if err == nil {
				capture := mgr.History.Capture(op)
				if capture != nil {
					for _, event := range v.Events {
						b, e := json.Marshal(map[string]any{"sourceTime": event.LastSeen, "type": event.Type, "reason": event.Reason, "message": event.Message, "count": event.Count})
						if e == nil {
							capture.Append(string(b) + "\n")
						}
					}
					capture.Close()
				}
			}
		case ShellExitMsg:
			err = v.Err
		case ShellSessionStartedMsg:
			outcome = "connected"
			v.Operation = op
			msg = v
		case PortForwardStartedMsg:
			err = v.Err
			outcome = "connected"
		case PortForwardStoppedMsg:
			err = v.Err
			outcome = "disconnected"
		}
		if !recordedResult {
			op.Finish(err, outcome)
		}
		return msg
	}
}
