package pushrepro

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/remotes/docker"
	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestLocalReproCommitDeadline(t *testing.T) {
	if os.Getenv("BUILDX_LOCAL_REPRO") != "1" {
		t.Skip("opt-in reproduction; fails if commit ignores its own deadline")
	}
	for _, stall := range []bool{false, true} {
		name := "healthy"
		if stall {
			name = "stalled_response"
		}
		t.Run(name, func(t *testing.T) {
			received := make(chan struct{})
			release := make(chan struct{})
			payload := []byte("local-repro-layer")
			dgst := digest.FromBytes(payload)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
				switch r.Method {
				case "HEAD":
					w.WriteHeader(404)
				case "POST":
					w.Header().Set("Location", "/v2/repro/blobs/uploads/one")
					w.WriteHeader(202)
				case "PUT":
					_, _ = io.Copy(io.Discard, r.Body)
					close(received)
					if stall {
						select {
						case <-release:
						case <-r.Context().Done():
							return
						}
					}
					w.Header().Set("Docker-Content-Digest", dgst.String())
					w.WriteHeader(201)
				default:
					w.WriteHeader(200)
				}
			}))
			defer server.Close()
			defer close(release)
			pushCtx, cancelPush := context.WithCancel(context.Background())
			defer cancelPush()
			resolver := docker.NewResolver(docker.ResolverOptions{PlainHTTP: true})
			pusher, err := resolver.Pusher(pushCtx, strings.TrimPrefix(server.URL, "http://")+"/repro:test")
			if err != nil {
				t.Fatal(err)
			}
			writer, err := pusher.Push(pushCtx, ocispec.Descriptor{Digest: dgst, Size: int64(len(payload)), MediaType: "application/vnd.oci.image.layer.v1.tar"})
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			if _, err = writer.Write(payload); err != nil {
				t.Fatal(err)
			}
			select {
			case <-received:
			case <-time.After(5 * time.Second):
				t.Fatal("upload barrier not reached")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- writer.Commit(ctx, int64(len(payload)), dgst) }()
			select {
			case err := <-done:
				if !stall && err != nil {
					t.Fatal(err)
				}
				if stall && err == nil {
					t.Fatal("stalled response unexpectedly succeeded")
				}
			case <-time.After(500 * time.Millisecond):
				cancelPush()
				select {
				case err := <-done:
					t.Logf("cancelling original Push context unblocked Commit: %v", err)
				case <-time.After(3 * time.Second):
					t.Error("Commit also failed to stop after cancelling Push")
				}
				t.Fatal("Commit ignored its own 100ms deadline while registry withheld response")
			}
		})
	}
}
