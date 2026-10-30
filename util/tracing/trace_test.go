package tracing

import (
	"os"
	"testing"

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
}

func clearCIEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"CI",
		"HOST_IP",
		"OTEL_TRACES_EXPORTER",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_PROTOCOL",
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
	ConfigureCI()

	require.Equal(t, "otlp", os.Getenv("OTEL_TRACES_EXPORTER"))
	require.Equal(t, "http://10.0.0.1:4317", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	require.Equal(t, "grpc", os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"))
	require.Equal(t, "ci.job.id=123", os.Getenv("OTEL_RESOURCE_ATTRIBUTES"))
}

func TestConfigureCIIPv6(t *testing.T) {
	clearCIEnv(t)

	t.Setenv("CI", "true")
	t.Setenv("HOST_IP", "2001:db8::1")
	ConfigureCI()

	require.Equal(t, "http://[2001:db8::1]:4317", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
}
