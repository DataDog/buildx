package execconn

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/docker/buildx/driver/kubernetes/kubeclient"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/apimachinery/pkg/util/httpstream/spdy"
	protocol "k8s.io/apimachinery/pkg/util/remotecommand"
	"k8s.io/client-go/rest"
)

func TestLocalReproSPDYTransport(t *testing.T) {
	if os.Getenv("BUILDX_LOCAL_REPRO") != "1" {
		t.Skip("opt-in loopback exec transport reproduction")
	}
	for _, clientCloses := range []bool{false, true} {
		name := "server_disconnect"
		if clientCloses {
			name = "client_close_cancels_transport"
		}
		t.Run(name, func(t *testing.T) {
			opened := make(chan httpstream.Connection, 1)
			handlerErrors := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := httpstream.Handshake(r, w, []string{protocol.StreamProtocolV4Name}); err != nil {
					handlerErrors <- err
					return
				}
				streams := make(chan httpstream.Stream, 4)
				connection := spdy.NewResponseUpgrader().UpgradeResponse(w, r, func(s httpstream.Stream, _ <-chan struct{}) error { streams <- s; return nil })
				if connection == nil {
					handlerErrors <- errors.New("SPDY upgrade failed")
					return
				}
				defer connection.Close()
				opened <- connection
				byType := map[string]httpstream.Stream{}
				for len(byType) < 4 {
					select {
					case s := <-streams:
						byType[s.Headers().Get(corev1.StreamType)] = s
					case <-connection.CloseChan():
						return
					case <-time.After(5 * time.Second):
						handlerErrors <- errors.New("stream setup timed out")
						return
					}
				}
				if _, err := byType[corev1.StreamTypeStdout].Write([]byte("READY")); err != nil {
					handlerErrors <- err
					return
				}
				<-connection.CloseChan()
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := &rest.Config{Host: server.URL, APIPath: "/api", ContentConfig: rest.ContentConfig{GroupVersion: &schema.GroupVersion{Version: "v1"}, NegotiatedSerializer: kubeclient.Codecs().WithoutConversion()}}
			restClient, err := rest.RESTClientFor(cfg)
			if err != nil {
				t.Fatal(err)
			}
			conn, err := ExecConn(ctx, restClient, cfg, "repro", "worker", "buildkitd", []string{"buildctl", "dial-stdio"})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			var wire httpstream.Connection
			select {
			case wire = <-opened:
			case err := <-handlerErrors:
				t.Fatal(err)
			case <-time.After(5 * time.Second):
				t.Fatal("exec connection never upgraded")
			}
			defer wire.Close()
			ready := make(chan error, 1)
			go func() {
				b := make([]byte, 5)
				_, err := io.ReadFull(conn, b)
				if err == nil && string(b) != "READY" {
					err = errors.New("unexpected control payload")
				}
				ready <- err
			}()
			select {
			case err := <-ready:
				if err != nil {
					t.Fatal(err)
				}
			case err := <-handlerErrors:
				t.Fatal(err)
			case <-time.After(5 * time.Second):
				t.Fatal("control payload was not delivered")
			}
			if clientCloses {
				if err := conn.Close(); err != nil {
					t.Fatal(err)
				}
				select {
				case <-wire.CloseChan():
				case <-time.After(time.Second):
					t.Fatal("client Close left real SPDY transport open")
				}
				if ctx.Err() != nil {
					t.Fatal("connection close cancelled parent build context")
				}
			} else {
				readDone := make(chan error, 1)
				go func() { _, err := conn.Read(make([]byte, 1)); readDone <- err }()
				wire.Close()
				select {
				case err := <-readDone:
					if err == nil {
						t.Fatal("disconnect produced a successful read")
					}
					t.Logf("server disconnect surfaced: %v", err)
				case <-time.After(time.Second):
					t.Fatal("read remained blocked after actual transport close")
				}
			}
		})
	}
}
