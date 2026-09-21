package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moby/buildkit/util/grpcerrors"
	"google.golang.org/grpc/codes"
)

// Retry the complete solve: losing the Status stream also cancels the BuildKit
// session, so reconnecting that RPC cannot resume the interrupted build.
func retryKubernetesBuild(ctx context.Context, enabled bool, delay time.Duration, run func() error, notify func(int, error)) error {
	for attempt := 0; ; attempt++ {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		err := run()
		if err == nil || !enabled || attempt == 2 || !kubernetesTransportEOF(err) {
			return err
		}
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		notify(attempt+1, err)
		timer := time.NewTimer(delay << attempt)
		select {
		case <-ctx.Done():
			timer.Stop()
			return context.Cause(ctx)
		case <-timer.C:
		}
	}
}

func kubernetesTransportEOF(err error) bool {
	st, ok := grpcerrors.AsGRPCStatus(err)
	if !ok || st == nil || st.Code() != codes.Unavailable {
		return false
	}
	// gRPC may wrap the transport error (for example "closing transport due to:
	// connection error: desc = \"error reading from server: EOF\", received prior
	// goaway: ..."), so match within the status message, not against all of it.
	msg := st.Message()
	return strings.Contains(msg, "error reading from server: EOF") ||
		strings.Contains(msg, "error reading from server: unexpected EOF")
}

// Builds may execute uncached steps again. Only restart builds with replayable
// inputs and image/cache-only outputs; never append to a consumed input stream
// or partially written local output. Explicit outputs also exclude default-load.
func replayableKubernetesBuild(driverType string, debugging bool, opts *BuildOptions) bool {
	if driverType != "kubernetes" || debugging || opts.Ref != "" || opts.CallFunc != nil || opts.ExportLoad || opts.ContextPath == "-" || opts.DockerfileName == "-" {
		return false
	}
	if !opts.ExportPush && len(opts.Exports) == 0 {
		return false
	}
	for _, output := range opts.Exports {
		if output.Destination != "" || output.Attrs["dest"] != "" {
			return false
		}
		switch output.Type {
		case "image", "registry", "cacheonly":
		default:
			return false
		}
	}
	for _, cache := range opts.CacheTo {
		if cache.Type == "local" {
			return false
		}
	}
	for _, path := range opts.NamedContexts {
		if path == "-" {
			return false
		}
	}
	for _, secret := range opts.Secrets {
		path := secret.FilePath
		if path == "" && secret.Env == "" && os.Getenv(secret.ID) == "" {
			path = secret.ID
		}
		if path != "" {
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				return false
			}
		}
	}
	// A Dockerfile can also be a FIFO or /dev/stdin without using "-",
	// including the implicit Dockerfile inside a local context.
	dockerfile := opts.DockerfileName
	contextInfo, contextErr := os.Stat(opts.ContextPath)
	localContext := contextErr == nil && contextInfo.IsDir()
	if dockerfile == "" && localContext {
		dockerfile = filepath.Join(opts.ContextPath, "Dockerfile")
		if _, err := os.Stat(dockerfile); os.IsNotExist(err) {
			dockerfile = filepath.Join(opts.ContextPath, "dockerfile")
		}
	}
	if dockerfile != "" && !strings.HasPrefix(dockerfile, "https://") && !strings.HasPrefix(dockerfile, "http://") {
		info, err := os.Stat(dockerfile)
		if (err != nil && localContext) || (err == nil && !info.Mode().IsRegular()) {
			return false
		}
	}
	return true
}
