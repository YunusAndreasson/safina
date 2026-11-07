package k8s

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

// DefaultDebugImage is what gets attached when a pod has no shell. netshoot
// is the on-caller's swiss-army-knife: curl, dig, tcpdump, jq, strace, vim.
// Larger than busybox (~600MB) but pulled lazily by the kubelet and worth
// the wait when you're already in a "shell didn't work" path.
const DefaultDebugImage = "nicolaka/netshoot"

// AttachDebugContainer adds an ephemeral container to pod that shares the
// process namespace of targetContainer, then attaches an interactive session
// to it. This is the in-Go equivalent of `kubectl debug -it pod/X
// --image=... --target=Y`.
//
// Note: ephemeral containers cannot be deleted by design (k8s API). They
// disappear only when the pod itself restarts. The shell modal surfaces this
// to the user in its hint row.
func AttachDebugContainer(
	ctx context.Context,
	cs kubernetes.Interface,
	restCfg *rest.Config,
	namespace, pod, targetContainer, image string,
) (*ShellSession, error) {
	if image == "" {
		image = DefaultDebugImage
	}

	name := "debugger-" + randSuffix(4)

	current, err := cs.CoreV1().Pods(namespace).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get pod: %w", err)
	}

	ec := corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:                     name,
			Image:                    image,
			ImagePullPolicy:          corev1.PullIfNotPresent,
			Stdin:                    true,
			TTY:                      true,
			TerminationMessagePolicy: corev1.TerminationMessageReadFile,
			Command:                  []string{"sh"},
		},
		TargetContainerName: targetContainer,
	}
	current.Spec.EphemeralContainers = append(current.Spec.EphemeralContainers, ec)

	if _, err = cs.CoreV1().Pods(namespace).UpdateEphemeralContainers(
		ctx, pod, current, metav1.UpdateOptions{},
	); err != nil {
		return nil, fmt.Errorf("create ephemeral container: %w", err)
	}

	if err = waitEphemeralRunning(ctx, cs, namespace, pod, name, 60*time.Second); err != nil {
		return nil, err
	}

	req := cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod).
		Namespace(namespace).
		SubResource("attach").
		VersionedParams(&corev1.PodAttachOptions{
			Container: name,
			Stdin:     true,
			Stdout:    true,
			Stderr:    false,
			TTY:       true,
		}, scheme.ParameterCodec)

	executor, err := newStreamExecutor(restCfg, req.URL())
	if err != nil {
		return nil, fmt.Errorf("create attach executor: %w", err)
	}

	return startSession(ctx, executor), nil
}

// waitEphemeralRunning polls the pod until the named ephemeral container is
// in Running state, or the timeout expires. We poll every 500ms — pulling a
// 600MB netshoot image on a cold node can take 10–30s, so we don't want to
// hammer the API server while waiting.
func waitEphemeralRunning(
	ctx context.Context,
	cs kubernetes.Interface,
	namespace, pod, ecName string,
	timeout time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("debug container %q did not start within %s", ecName, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		cur, err := cs.CoreV1().Pods(namespace).Get(ctx, pod, metav1.GetOptions{})
		if err != nil {
			continue
		}
		for _, st := range cur.Status.EphemeralContainerStatuses {
			if st.Name != ecName {
				continue
			}
			if st.State.Running != nil {
				return nil
			}
			if st.State.Terminated != nil {
				reason := st.State.Terminated.Reason
				if reason == "" {
					reason = "unknown"
				}
				return fmt.Errorf("debug container terminated: %s", reason)
			}
		}
	}
}
