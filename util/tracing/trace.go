package tracing

import (
	"context"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/moby/buildkit/util/tracing/delegated"
	"github.com/moby/buildkit/util/tracing/detect"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	otelExporterEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"
	otelExporterProtocol = "OTEL_EXPORTER_OTLP_PROTOCOL"
	otelResourceAttrs    = "OTEL_RESOURCE_ATTRIBUTES"
)

// ConfigureCI configures the Buildx process to export OTLP traces for CI
// builds. It must run before TraceCurrentCommand, which reads the OTEL
// configuration from the environment. Existing OTEL configuration is
// preserved.
func ConfigureCI() {
	lookup := os.LookupEnv
	if ci, ok := lookup("CI"); !ok || strings.TrimSpace(ci) == "" {
		return
	}

	if _, ok := lookup(otelExporterEndpoint); !ok {
		// Resolve HOST_IP here instead of passing the Kubernetes $(HOST_IP)
		// expression to the Buildx process. That expression is only expanded by
		// Kubernetes when it injects a container environment.
		host, ok := lookup("HOST_IP")
		host = strings.TrimSpace(host)
		if !ok || host == "" {
			return
		}
		setenvIfMissing(otelExporterEndpoint, "http://"+net.JoinHostPort(host, "4317"), lookup)
	}
	setenvIfMissing("OTEL_TRACES_EXPORTER", "otlp", lookup)
	setenvIfMissing(otelExporterProtocol, "grpc", lookup)
	if _, ok := lookup(otelResourceAttrs); !ok {
		if attrs := ciResourceAttributes(lookup); attrs != "" {
			_ = os.Setenv(otelResourceAttrs, attrs)
		}
	}
}

func setenvIfMissing(name, value string, lookup func(string) (string, bool)) {
	if _, ok := lookup(name); !ok {
		_ = os.Setenv(name, value)
	}
}

func ciResourceAttributes(lookup func(string) (string, bool)) string {
	fields := []struct {
		attribute string
		env       string
	}{
		{"service.namespace", "POD_NAMESPACE"},
		{"kubernetes.cluster", "K8S_CLUSTER_NAME"},
		{"kubernetes.namespace", "POD_NAMESPACE"},
		{"ci.job.id", "CI_JOB_ID"},
		{"ci.job.name", "CI_JOB_NAME"},
		{"ci.job.url", "CI_JOB_URL"},
		{"ci.job.stage", "CI_JOB_STAGE"},
		{"ci.pipeline.id", "CI_PIPELINE_ID"},
		{"ci.pipeline.url", "CI_PIPELINE_URL"},
		{"git.repository.name", "CI_PROJECT_PATH"},
		{"git.commit.sha", "CI_COMMIT_SHA"},
		{"git.branch", "CI_COMMIT_REF_NAME"},
		{"git.commit.author", "CI_COMMIT_AUTHOR"},
		{"ci.runner.id", "CI_RUNNER_ID"},
	}

	attrs := make([]string, 0, len(fields))
	for _, field := range fields {
		if value, ok := lookup(field.env); ok && value != "" {
			attrs = append(attrs, field.attribute+"="+url.PathEscape(value))
		}
	}
	return strings.Join(attrs, ",")
}

func TraceCurrentCommand(ctx context.Context, args []string, attrs ...attribute.KeyValue) (context.Context, func(error), error) {
	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(detect.Resource()),
		sdktrace.WithBatcher(delegated.DefaultExporter),
	}
	if exp, err := detect.NewSpanExporter(ctx); err != nil {
		otel.Handle(err)
	} else if !detect.IsNoneSpanExporter(exp) {
		opts = append(opts, sdktrace.WithBatcher(exp))
	}

	tp := sdktrace.NewTracerProvider(opts...)
	ctx, span := tp.Tracer("").Start(ctx, strings.Join(args, " "), trace.WithAttributes(
		attrs...,
	))

	return ctx, func(err error) {
		if err != nil {
			span.RecordError(err)
		}
		span.End()

		_ = tp.Shutdown(context.TODO())
	}, nil
}
