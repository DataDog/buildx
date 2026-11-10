package commands

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/cli/cli/command"
	"github.com/moby/buildkit/util/tracing/detect"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// Keep exported evidence after the command shuts its provider down.
type createSpanExporter struct{ *tracetest.InMemoryExporter }

func (*createSpanExporter) Shutdown(context.Context) error { return nil }

func TestCreateExportsEarlyFailure(t *testing.T) {
	for _, storeFailure := range []bool{false, true} {
		name := "invalid_driver"
		if storeFailure {
			name = "store_failure"
		}
		t.Run(name, func(t *testing.T) {
			exporter := &createSpanExporter{tracetest.NewInMemoryExporter()}
			detect.Register("buildx-create-test", detect.TraceExporterDetector(func() (sdktrace.SpanExporter, error) {
				if os.Getenv("OTEL_TRACES_EXPORTER") != "buildx-create-test" {
					return nil, nil
				}
				return exporter, nil
			}), 1000)
			t.Setenv("CI", "true")
			t.Setenv("HOST_IP", "127.0.0.1")
			t.Setenv("CI_JOB_ID", "create-test")
			t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
			t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
			t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
			t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "")
			t.Setenv("OTEL_TRACES_EXPORTER", "buildx-create-test")
			dir := filepath.Join(t.TempDir(), "config")
			if storeFailure {
				require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0600))
			}
			t.Setenv("BUILDX_CONFIG", dir)
			cli, err := command.NewDockerCli()
			require.NoError(t, err)
			err = runCreate(context.Background(), cli, createOptions{name: "test", driver: "missing-driver", actionAppend: true, bootstrap: true}, nil)
			require.Error(t, err)
			spans := exporter.GetSpans()
			require.Len(t, spans, 1)
			require.Equal(t, "buildx.builder.create", spans[0].Name)
			require.Equal(t, "http://127.0.0.1:4317", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
			job, ok := spans[0].Resource.Set().Value("ci.job.id")
			require.True(t, ok)
			require.Equal(t, "create-test", job.AsString())
			require.Equal(t, codes.Error, spans[0].Status.Code)
			attrs := attribute.NewSet(spans[0].Attributes...)
			outcome, _ := attrs.Value("buildx.create.outcome")
			require.Equal(t, "error", outcome.AsString())
			driver, _ := attrs.Value("driver")
			require.Equal(t, "missing-driver", driver.AsString())
			appendMode, _ := attrs.Value("buildx.create.append")
			require.True(t, appendMode.AsBool())
			bootstrap, _ := attrs.Value("buildx.create.bootstrap")
			require.True(t, bootstrap.AsBool())
			leave, _ := attrs.Value("buildx.create.leave")
			require.False(t, leave.AsBool())
			require.False(t, spans[0].EndTime.Before(spans[0].StartTime))
		})
	}
}
