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

	require.Equal(t, "ci.job.id=123,git.repository.name=org/project", ciResourceAttributes(lookup(map[string]string{
		"CI_JOB_ID":       "123",
		"CI_PROJECT_PATH": "org/project",
	})))
}

func TestConfigureCI(t *testing.T) {
	for _, key := range []string{
		"CI",
		"OTEL_TRACES_EXPORTER",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_PROTOCOL",
		"OTEL_RESOURCE_ATTRIBUTES",
	} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}

	t.Setenv("CI", "true")
	t.Setenv("CI_JOB_ID", "123")
	t.Setenv("HOST_IP", "10.0.0.1")
	ConfigureCI()

	require.Equal(t, "otlp", os.Getenv("OTEL_TRACES_EXPORTER"))
	require.Equal(t, "http://10.0.0.1:4317", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	require.Equal(t, "grpc", os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"))
	require.Equal(t, "ci.job.id=123", os.Getenv("OTEL_RESOURCE_ATTRIBUTES"))
}
