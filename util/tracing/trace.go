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
	otelExporterEndpoint       = "OTEL_EXPORTER_OTLP_ENDPOINT"
	otelExporterProtocol       = "OTEL_EXPORTER_OTLP_PROTOCOL"
	otelTracesExporterEndpoint = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	otelTracesExporterProtocol = "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"
	otelResourceAttrs          = "OTEL_RESOURCE_ATTRIBUTES"
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

	unsetenvIfBlank(otelTracesExporterEndpoint, lookup)
	unsetenvIfBlank(otelTracesExporterProtocol, lookup)

	endpoint, _ := lookup(otelExporterEndpoint)
	tracesEndpoint, _ := lookup(otelTracesExporterEndpoint)
	if strings.TrimSpace(endpoint) == "" && strings.TrimSpace(tracesEndpoint) == "" {
		// Resolve HOST_IP here instead of passing the Kubernetes $(HOST_IP)
		// expression to the Buildx process. That expression is only expanded by
		// Kubernetes when it injects a container environment.
		host, ok := lookup("HOST_IP")
		host = strings.TrimSpace(host)
		if !ok || host == "" {
			return
		}
		setenvIfEmpty(otelExporterEndpoint, "http://"+net.JoinHostPort(host, defaultOTLPPort(lookup)), lookup)
	}
	setenvIfEmpty("OTEL_TRACES_EXPORTER", "otlp", lookup)
	setenvIfEmpty(otelExporterProtocol, "grpc", lookup)
	existingAttrs, _ := lookup(otelResourceAttrs)
	if strings.TrimSpace(existingAttrs) == "" {
		existingAttrs = ""
	}
	if attrs := mergeCIResourceAttributes(existingAttrs, lookup); attrs != "" && attrs != existingAttrs {
		_ = os.Setenv(otelResourceAttrs, attrs)
	}
}

func unsetenvIfBlank(name string, lookup func(string) (string, bool)) {
	if value, ok := lookup(name); ok && strings.TrimSpace(value) == "" {
		_ = os.Unsetenv(name)
	}
}

func defaultOTLPPort(lookup func(string) (string, bool)) string {
	protocol, ok := lookup(otelTracesExporterProtocol)
	if !ok || strings.TrimSpace(protocol) == "" {
		protocol, _ = lookup(otelExporterProtocol)
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(protocol)), "http") {
		return "4318"
	}
	return "4317"
}

func setenvIfEmpty(name, value string, lookup func(string) (string, bool)) {
	if existing, _ := lookup(name); strings.TrimSpace(existing) == "" {
		_ = os.Setenv(name, value)
	}
}

func ciResourceAttributes(lookup func(string) (string, bool)) string {
	return mergeCIResourceAttributes("", lookup)
}

func mergeCIResourceAttributes(existing string, lookup func(string) (string, bool)) string {
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

	existingKeys := make(map[string]struct{})
	for _, pair := range strings.Split(existing, ",") {
		if key, _, ok := strings.Cut(pair, "="); ok {
			existingKeys[strings.TrimSpace(key)] = struct{}{}
		}
	}

	attrs := make([]string, 0, len(fields)+1)
	if existing != "" {
		attrs = append(attrs, existing)
	}
	for _, field := range fields {
		if _, ok := existingKeys[field.attribute]; ok {
			continue
		}
		if value, ok := lookup(field.env); ok && strings.TrimSpace(value) != "" {
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
