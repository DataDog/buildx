package podchooser

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestRejectUnreadyWorker(t *testing.T) {
	for _, mode := range []string{"random", "sticky"} {
		t.Run(mode, func(t *testing.T) {
			pod := newPod("worker", corev1.PodRunning)
			client := &fakePodClient{pods: []corev1.Pod{pod}}
			var chooser PodChooser = &RandomPodChooser{PodClient: client, Deployment: newDeployment()}
			if mode == "sticky" {
				chooser = &StickyPodChooser{Key: "same-build", PodClient: client, Deployment: newDeployment()}
			}
			selected, err := chooser.ChoosePod(context.Background())
			require.NoError(t, err)
			require.Equal(t, pod.Name, selected.Name)
			t.Logf("control: Running/Ready=True worker selected: %s", selected.Name)
			for _, condition := range []corev1.ConditionStatus{corev1.ConditionFalse, corev1.ConditionUnknown, ""} {
				client.pods[0].Status.Conditions = nil
				if condition != "" {
					client.pods[0].Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: condition}}
				}
				selected, err = chooser.ChoosePod(context.Background())
				if err == nil {
					t.Fatalf("selected unready pod %q with Ready=%q; expected no eligible workers", selected.Name, condition)
				}
				require.EqualError(t, err, "no ready buildkit pods found")
				t.Logf("fault: Running/Ready=%q worker rejected: %v", condition, err)
			}
			client.pods[0].Status.Conditions = pod.Status.Conditions
			selected, err = chooser.ChoosePod(context.Background())
			require.NoError(t, err)
			require.Equal(t, pod.Name, selected.Name)
			t.Logf("recovery: same worker selected after Ready=True: %s", selected.Name)
		})
	}
}
