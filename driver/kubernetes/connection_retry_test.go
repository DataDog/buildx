package kubernetes

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/buildx/driver/kubernetes/kubeclient"
	"github.com/docker/buildx/driver/kubernetes/podchooser"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
)

type retryPods struct {
	client rest.Interface
	pods   []corev1.Pod
}

func (p *retryPods) RESTClient() rest.Interface { return p.client }
func (p *retryPods) List(context.Context, metav1.ListOptions) (*corev1.PodList, error) {
	return &corev1.PodList{Items: append([]corev1.Pod(nil), p.pods...)}, nil
}

func TestDialReselectsAfterStreamSetupFailure(t *testing.T) {
	for _, sticky := range []bool{false, true} {
		t.Run(fmt.Sprint(sticky), func(t *testing.T) {
			var mu sync.Mutex
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.URL.Path)
				attempt := len(paths)
				mu.Unlock()
				if attempt == 1 {
					http.Error(w, "io: read/write on closed pipe", http.StatusServiceUnavailable)
					return
				}
				http.Error(w, "forbidden by test policy", http.StatusForbidden)
			}))
			defer server.Close()
			cfg := &rest.Config{Host: server.URL, APIPath: "/api", ContentConfig: rest.ContentConfig{GroupVersion: &schema.GroupVersion{Version: "v1"}, NegotiatedSerializer: kubeclient.Codecs().WithoutConversion()}}
			client, err := rest.RESTClientFor(cfg)
			require.NoError(t, err)
			pods := &retryPods{client: client}
			for _, name := range []string{"worker-a", "worker-b"} {
				pods.pods = append(pods.pods, corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "test"}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "buildkitd"}}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}})
			}
			var chooser podchooser.PodChooser = &podchooser.RandomPodChooser{PodClient: pods}
			if sticky {
				chooser = &podchooser.StickyPodChooser{Key: "same-build", PodClient: pods}
			}
			d := &Driver{clientConfig: &mockClientConfig{clientConfig: cfg}, podClient: pods, podChooser: chooser}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, err := d.Dial(ctx)
			require.ErrorContains(t, err, "forbidden")
			require.Nil(t, conn)
			mu.Lock()
			defer mu.Unlock()
			require.Len(t, paths, 2, "authorization errors must not be retried")
			require.NotEqual(t, paths[0], paths[1], "retry must select another eligible worker")
		})
	}
}
func TestConnectionRetryClassification(t *testing.T) {
	for _, err := range []error{io.ErrClosedPipe, fmt.Errorf("wrapped: %w", io.EOF), fmt.Errorf("failed to write client preface: connection lost"), fmt.Errorf("error reading server preface: EOF")} {
		require.True(t, isTransientConnectionError(err), err.Error())
	}
	for _, err := range []error{context.Canceled, fmt.Errorf("forbidden"), fmt.Errorf("x509: certificate signed by unknown authority"), fmt.Errorf("Dockerfile process exited 42")} {
		require.False(t, isTransientConnectionError(err), err.Error())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := tryWithBackoff(ctx, "worker", func() error { calls++; return io.ErrClosedPipe })
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, calls)
}
func TestPodAvoidancePreservesSingleWorkerRetry(t *testing.T) {
	p := &retryPods{pods: []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "only"}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}}}
	pods, err := podchooser.ListRunningPods(context.Background(), p, nil, nil, "only")
	require.NoError(t, err)
	require.Len(t, pods, 1)
	require.True(t, strings.HasPrefix(pods[0].Name, "only"))
}
