package podchooser

import (
	"context"
	corev1 "k8s.io/api/core/v1"
	"testing"
)

func TestRejectUnreadyWorker(t *testing.T) {
	pod := newPod("only-unready-worker", corev1.PodRunning)
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}
	client := &fakePodClient{pods: []corev1.Pod{pod}}
	for name, chooser := range map[string]PodChooser{
		"random": &RandomPodChooser{PodClient: client, Deployment: newDeployment()},
		"sticky": &StickyPodChooser{Key: "repro", PodClient: client, Deployment: newDeployment()},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := chooser.ChoosePod(context.Background())
			if err == nil {
				t.Fatalf("selected unready pod %q; expected no eligible workers", got.Name)
			}
		})
	}
}
