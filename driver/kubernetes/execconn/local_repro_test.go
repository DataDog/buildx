package execconn

import (
	"context"
	"k8s.io/client-go/tools/remotecommand"
	"os"
	"testing"
	"time"
)

type localReproExecutor struct{ started, stopped chan struct{} }

func (f *localReproExecutor) Stream(remotecommand.StreamOptions) error { panic("not used") }
func (f *localReproExecutor) StreamWithContext(ctx context.Context, _ remotecommand.StreamOptions) error {
	close(f.started)
	defer close(f.stopped)
	<-ctx.Done()
	return ctx.Err()
}
func TestLocalReproCloseStopsStream(t *testing.T) {
	if os.Getenv("BUILDX_LOCAL_REPRO") != "1" {
		t.Skip("opt-in reproduction: expects safe behavior; fails on affected baseline")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &localReproExecutor{started: make(chan struct{}), stopped: make(chan struct{})}
	conn := newExecConn(ctx, f)
	select {
	case <-f.started:
	case <-time.After(5 * time.Second):
		t.Fatal("stream never started")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.stopped:
	case <-time.After(time.Second):
		t.Fatal("connection closed but executor still waiting on its context")
	}
	if ctx.Err() != nil {
		t.Fatal("connection close cancelled parent build context")
	}
}
