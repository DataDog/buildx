package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/buildx/util/buildflags"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestKubernetesBuildRecovery(t *testing.T) {
	eof := status.Error(codes.Unavailable, "error reading from server: EOF")
	t.Run("restarts whole build then succeeds", func(t *testing.T) {
		calls, notifications := 0, 0
		err := retryKubernetesBuild(context.Background(), true, 0, func() error {
			calls++
			if calls == 1 {
				return fmt.Errorf("failed to receive status: %w", eof)
			}
			return nil
		}, func(attempt int, err error) {
			notifications++
			require.Equal(t, 1, attempt)
			require.ErrorIs(t, err, eof)
		})
		require.NoError(t, err)
		require.Equal(t, 2, calls)
		require.Equal(t, 1, notifications)
	})
	t.Run("persistent EOF exhausts two retries", func(t *testing.T) {
		calls := 0
		err := retryKubernetesBuild(context.Background(), true, 0, func() error { calls++; return eof }, func(int, error) {})
		require.ErrorIs(t, err, eof)
		require.Equal(t, 3, calls)
	})
	t.Run("cancellation interrupts backoff", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		err := retryKubernetesBuild(ctx, true, time.Hour, func() error { calls++; return eof }, func(int, error) { cancel() })
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 1, calls)
	})
	for name, msg := range map[string]string{
		"wrapped EOF with goaway": `closing transport due to: connection error: desc = "error reading from server: EOF", received prior goaway: code: NO_ERROR, debug data: "graceful_stop"`,
		"wrapped unexpected EOF":  `connection error: desc = "error reading from server: unexpected EOF"`,
	} {
		t.Run(name, func(t *testing.T) {
			wrapped := fmt.Errorf("failed to receive status: %w", status.Error(codes.Unavailable, msg))
			calls := 0
			err := retryKubernetesBuild(context.Background(), true, 0, func() error {
				calls++
				if calls == 1 {
					return wrapped
				}
				return nil
			}, func(int, error) {})
			require.NoError(t, err)
			require.Equal(t, 2, calls)
		})
	}
	for name, failure := range map[string]error{
		"connection refused":    status.Error(codes.Unavailable, `connection error: desc = "transport: Error while dialing: connection refused"`),
		"Dockerfile failure":    errors.New("process did not complete successfully: exit code: 1"),
		"HTTP 429":              status.Error(codes.Unavailable, "unexpected status: 429 Too Many Requests"),
		"arbitrary unavailable": status.Error(codes.Unavailable, "service unavailable"),
		"misleading output":     errors.New("RUN echo error reading from server: EOF"),
		"application EOF":       status.Error(codes.Unknown, "error reading from server: EOF"),
		"plain EOF":             io.EOF,
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			err := retryKubernetesBuild(context.Background(), true, 0, func() error { calls++; return failure }, func(int, error) { t.Fatal("unexpected retry") })
			require.ErrorIs(t, err, failure)
			require.Equal(t, 1, calls)
		})
	}
	t.Run("disabled", func(t *testing.T) {
		calls := 0
		require.ErrorIs(t, retryKubernetesBuild(context.Background(), false, 0, func() error { calls++; return eof }, func(int, error) { t.Fatal("unexpected retry") }), eof)
		require.Equal(t, 1, calls)
	})
}

func TestReplayableKubernetesBuild(t *testing.T) {
	contextDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, "Dockerfile"), []byte("FROM scratch"), 0600))
	valid := func() *BuildOptions { return &BuildOptions{ContextPath: contextDir, ExportPush: true} }
	require.True(t, replayableKubernetesBuild("kubernetes", false, valid()))
	require.False(t, replayableKubernetesBuild("remote", false, valid()))
	require.False(t, replayableKubernetesBuild("kubernetes", true, valid()))
	for name, change := range map[string]func(*BuildOptions){
		"stdin context":         func(o *BuildOptions) { o.ContextPath = "-" },
		"stdin Dockerfile":      func(o *BuildOptions) { o.DockerfileName = "-" },
		"named stdin":           func(o *BuildOptions) { o.NamedContexts = map[string]string{"src": "-"} },
		"explicit ref":          func(o *BuildOptions) { o.Ref = "existing-solve" },
		"load":                  func(o *BuildOptions) { o.ExportLoad = true },
		"implicit default-load": func(o *BuildOptions) { o.ExportPush = false },
		"local export":          func(o *BuildOptions) { o.Exports = buildflags.Exports{&buildflags.ExportEntry{Type: "local"}} },
		"stdout export": func(o *BuildOptions) {
			o.Exports = buildflags.Exports{&buildflags.ExportEntry{Type: "tar", Destination: "-"}}
		},
		"local cache":       func(o *BuildOptions) { o.CacheTo = []*buildflags.CacheOptionsEntry{{Type: "local"}} },
		"stream secret":     func(o *BuildOptions) { o.Secrets = buildflags.Secrets{&buildflags.Secret{FilePath: os.DevNull}} },
		"stream Dockerfile": func(o *BuildOptions) { o.DockerfileName = os.DevNull },
	} {
		t.Run(name, func(t *testing.T) {
			opts := valid()
			change(opts)
			require.False(t, replayableKubernetesBuild("kubernetes", false, opts))
		})
	}
	t.Run("regular secret", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "secret")
		require.NoError(t, os.WriteFile(path, []byte("test"), 0600))
		opts := valid()
		opts.Secrets = buildflags.Secrets{&buildflags.Secret{FilePath: path}}
		require.True(t, replayableKubernetesBuild("kubernetes", false, opts))
	})
	for _, name := range []string{"Dockerfile", "dockerfile"} {
		t.Run("implicit nonregular "+name, func(t *testing.T) {
			opts := valid()
			opts.ContextPath = t.TempDir()
			require.NoError(t, os.Symlink(os.DevNull, filepath.Join(opts.ContextPath, name)))
			require.False(t, replayableKubernetesBuild("kubernetes", false, opts))
		})
	}
}
