package tracing

import (
	"context"
	"os"
	"testing"

	"github.com/moby/buildkit/util/tracing/detect"
	"github.com/stretchr/testify/require"
)

func TestCIResourceAttributes(t *testing.T) {
	lookup := func(values map[string]string) func(string) (string, bool) {
		return func(key string) (string, bool) {
			value, ok := values[key]
			return value, ok
		}
	}

	require.Equal(t, "ci.job.name=package-oci:%20%5Blinux%2C%20amd64%5D%25,git.repository.name=org%2Fproject", ciResourceAttributes(lookup(map[string]string{
		"CI_JOB_NAME":     "package-oci: [linux, amd64]%",
		"CI_PROJECT_PATH": "org/project",
	})))

	require.Equal(t, "docker.cli.cobra.command_path=buildx%20build,ci.job.id=explicit,ci.pipeline.id=456", mergeCIResourceAttributes(
		"docker.cli.cobra.command_path=buildx%20build,ci.job.id=explicit",
		lookup(map[string]string{
			"CI_JOB_ID":      "123",
			"CI_PIPELINE_ID": "456",
		}),
	))
}

func clearCIEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"CI",
		"HOST_IP",
		"OTEL_TRACES_EXPORTER",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_PROTOCOL",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL",
		"OTEL_RESOURCE_ATTRIBUTES",
		"POD_NAMESPACE",
		"K8S_CLUSTER_NAME",
		"CI_JOB_ID",
		"CI_JOB_NAME",
		"CI_JOB_URL",
		"CI_JOB_STAGE",
		"CI_PIPELINE_ID",
		"CI_PIPELINE_URL",
		"CI_PROJECT_PATH",
		"CI_COMMIT_SHA",
		"CI_COMMIT_REF_NAME",
		"CI_COMMIT_AUTHOR",
		"CI_RUNNER_ID",
	} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
}

func TestConfigureCI(t *testing.T) {
	clearCIEnv(t)

	t.Setenv("CI", "true")
	t.Setenv("CI_JOB_ID", "123")
	t.Setenv("HOST_IP", "10.0.0.1")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "docker.cli.cobra.command_path=buildx%20build")
	ConfigureCI()

	require.Equal(t, "otlp", os.Getenv("OTEL_TRACES_EXPORTER"))
	require.Equal(t, "http://10.0.0.1:4317", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	require.Equal(t, "grpc", os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"))
	require.Equal(t, "docker.cli.cobra.command_path=buildx%20build,ci.job.id=123", os.Getenv("OTEL_RESOURCE_ATTRIBUTES"))
}

func TestConfigureCIIPv6(t *testing.T) {
	clearCIEnv(t)

	t.Setenv("CI", "true")
	t.Setenv("HOST_IP", "2001:db8::1")
	ConfigureCI()

	require.Equal(t, "http://[2001:db8::1]:4317", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
}

func TestConfigureCIHTTPProtocol(t *testing.T) {
	for _, protocolEnv := range []string{otelExporterProtocol, otelTracesExporterProtocol} {
		t.Run(protocolEnv, func(t *testing.T) {
			clearCIEnv(t)

			t.Setenv("CI", "true")
			t.Setenv("HOST_IP", "10.0.0.1")
			t.Setenv(protocolEnv, "http/protobuf")
			ConfigureCI()

			require.Equal(t, "http://10.0.0.1:4318", os.Getenv(otelExporterEndpoint))
		})
	}
}

func TestConfigureCITracesEndpoint(t *testing.T) {
	clearCIEnv(t)

	t.Setenv("CI", "true")
	t.Setenv("CI_JOB_ID", "123")
	t.Setenv(otelTracesExporterEndpoint, "http://collector:4318/v1/traces")
	ConfigureCI()

	require.Empty(t, os.Getenv(otelExporterEndpoint))
	require.Equal(t, "ci.job.id=123", os.Getenv(otelResourceAttrs))
}

func TestConfigureCIEmptyOTelValues(t *testing.T) {
	clearCIEnv(t)

	t.Setenv("CI", "true")
	t.Setenv("CI_JOB_ID", "123")
	t.Setenv("HOST_IP", "10.0.0.1")
	for _, key := range []string{
		"OTEL_TRACES_EXPORTER",
		otelExporterEndpoint,
		otelExporterProtocol,
		otelTracesExporterEndpoint,
		otelTracesExporterProtocol,
		otelResourceAttrs,
	} {
		t.Setenv(key, " \t ")
	}
	ConfigureCI()

	require.Equal(t, "otlp", os.Getenv("OTEL_TRACES_EXPORTER"))
	require.Equal(t, "http://10.0.0.1:4317", os.Getenv(otelExporterEndpoint))
	require.Equal(t, "grpc", os.Getenv(otelExporterProtocol))
	require.Equal(t, "ci.job.id=123", os.Getenv(otelResourceAttrs))
	require.Empty(t, os.Getenv(otelTracesExporterEndpoint))
	require.Empty(t, os.Getenv(otelTracesExporterProtocol))

	exporter, err := detect.NewSpanExporter(context.Background())
	require.NoError(t, err)
	require.NotNil(t, exporter)
	require.NoError(t, exporter.Shutdown(context.Background()))
}
