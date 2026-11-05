package progress

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/moby/buildkit/client"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type discardStartupWriter struct{ Writer }

func (discardStartupWriter) Write(*client.SolveStatus) {}

func startupPoint(t *testing.T, reader *sdkmetric.ManualReader) metricdata.HistogramDataPoint[float64] {
	t.Helper()
	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &data))
	require.Len(t, data.ScopeMetrics, 1)
	require.Len(t, data.ScopeMetrics[0].Metrics, 1)
	h := data.ScopeMetrics[0].Metrics[0].Data.(metricdata.Histogram[float64])
	require.Len(t, h.DataPoints, 1)
	require.Equal(t, uint64(1), h.DataPoints[0].Count)
	require.GreaterOrEqual(t, h.DataPoints[0].Sum, 0.0)
	return h.DataPoints[0]
}

func TestBuildStartup(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		progress bool
		outcome  string
	}{
		{name: "progress", progress: true, outcome: "first_progress"},
		{name: "failure_after_progress", progress: true, err: fmt.Errorf("transport failed"), outcome: "first_progress"},
		{name: "initialization_failure", err: fmt.Errorf("initialize builder"), outcome: "error"},
		{name: "canceled", err: fmt.Errorf("bootstrap: %w", context.Canceled), outcome: "canceled"},
		{name: "deadline", err: fmt.Errorf("bootstrap: %w", context.DeadlineExceeded), outcome: "deadline_exceeded"},
		{name: "no_progress", outcome: "completed_without_progress"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := sdkmetric.NewManualReader()
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			defer mp.Shutdown(context.Background())
			ctx, cancel := context.WithCancel(context.Background())
			ctx, finish := TrackBuildStartup(ctx, mp)
			writer := WithBuildStartup(ctx, discardStartupWriter{})
			writer.Write(&client.SolveStatus{})
			writer.Write(&client.SolveStatus{Vertexes: []*client.Vertex{{Name: "pending"}}})
			if tc.progress {
				// Concurrent streams from several platforms still yield one sample.
				now := time.Now()
				var wg sync.WaitGroup
				for range 20 {
					wg.Go(func() { writer.Write(&client.SolveStatus{Vertexes: []*client.Vertex{{Started: &now}}}) })
				}
				wg.Wait()
			}
			cancel()
			finish(tc.err)
			finish(tc.err)
			p := startupPoint(t, reader)
			value, ok := p.Attributes.Value("outcome")
			require.True(t, ok)
			require.Equal(t, tc.outcome, value.AsString())
		})
	}
}

func TestBuildStartupIgnoresClientProgress(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer mp.Shutdown(context.Background())
	ctx, finish := TrackBuildStartup(context.Background(), mp)
	base := discardStartupWriter{}
	// Bootstrap writes go directly to the printer, outside the solver wrapper.
	require.NoError(t, Write(base, "booting buildkit", func() error { return nil }))
	WithBuildStartup(ctx, base).Write(&client.SolveStatus{})
	finish(context.DeadlineExceeded)
	p := startupPoint(t, reader)
	value, _ := p.Attributes.Value("outcome")
	require.Equal(t, "deadline_exceeded", value.AsString())
	require.Equal(t, base, WithBuildStartup(context.Background(), base))
}
