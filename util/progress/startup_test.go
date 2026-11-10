package progress

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/moby/buildkit/client"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type discardStartupWriter struct{ Writer }

func (discardStartupWriter) Write(*client.SolveStatus) {}

func startupContext(t *testing.T) (context.Context, *tracetest.SpanRecorder) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	ctx, parent := provider.Tracer("test").Start(context.Background(), "build .")
	t.Cleanup(func() { parent.End(); require.NoError(t, provider.Shutdown(context.Background())) })
	return ctx, recorder
}

func startupSpan(t *testing.T, recorder *tracetest.SpanRecorder, outcome string) sdktrace.ReadOnlySpan {
	t.Helper()
	spans := recorder.Ended()
	require.Len(t, spans, 1)
	span := spans[0]
	require.Equal(t, "buildx.build.startup", span.Name())
	attrs := attribute.NewSet(span.Attributes()...)
	value, ok := attrs.Value("buildx.startup.outcome")
	require.True(t, ok)
	require.Equal(t, outcome, value.AsString())
	require.False(t, span.EndTime().Before(span.StartTime()))
	return span
}

func TestBuildStartup(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		progress bool
		outcome  string
		status   codes.Code
	}{
		{name: "progress", progress: true, outcome: "first_progress"},
		{name: "failure_after_progress", progress: true, err: fmt.Errorf("transport failed"), outcome: "first_progress"},
		{name: "initialization_failure", err: fmt.Errorf("initialize builder"), outcome: "error", status: codes.Error},
		{name: "canceled", err: fmt.Errorf("bootstrap: %w", context.Canceled), outcome: "canceled", status: codes.Error},
		{name: "deadline", err: fmt.Errorf("bootstrap: %w", context.DeadlineExceeded), outcome: "deadline_exceeded", status: codes.Error},
		{name: "no_progress", outcome: "completed_without_progress"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent, recorder := startupContext(t)
			ctx, cancel := context.WithCancel(parent)
			start := time.Now().Add(-time.Second)
			ctx, finish := TrackBuildStartup(ctx, start)
			// Do not reparent the entire solve beneath a short-lived startup span.
			require.Equal(t, trace.SpanFromContext(parent), trace.SpanFromContext(ctx))
			SetBuildStartupDriver(ctx, "kubernetes")
			writer := WithBuildStartup(ctx, discardStartupWriter{})
			writer.Write(&client.SolveStatus{})
			writer.Write(&client.SolveStatus{Vertexes: []*client.Vertex{{Name: "pending"}}})
			require.Empty(t, recorder.Ended())
			if tc.progress {
				// Cached vertices and concurrent platform streams still yield one span.
				now := time.Now()
				var wg sync.WaitGroup
				for range 20 {
					wg.Go(func() { writer.Write(&client.SolveStatus{Vertexes: []*client.Vertex{{Started: &now, Cached: true}}}) })
				}
				wg.Wait()
				startupSpan(t, recorder, "first_progress") // Ends before command completion.
			}
			cancel()
			finish(tc.err)
			finish(tc.err)
			span := startupSpan(t, recorder, tc.outcome)
			require.Equal(t, start, span.StartTime())
			require.Equal(t, trace.SpanFromContext(parent).SpanContext().SpanID(), span.Parent().SpanID())
			require.Equal(t, tc.status, span.Status().Code)
			attrs := attribute.NewSet(span.Attributes()...)
			driver, _ := attrs.Value("driver")
			require.Equal(t, "kubernetes", driver.AsString())
		})
	}
}

func TestBuildStartupIgnoresClientProgress(t *testing.T) {
	ctx, recorder := startupContext(t)
	ctx, finish := TrackBuildStartup(ctx, time.Now())
	base := discardStartupWriter{}
	// Bootstrap writes go directly to the printer, outside the solver wrapper.
	require.NoError(t, Write(base, "booting buildkit", func() error { return nil }))
	WithBuildStartup(ctx, base).Write(&client.SolveStatus{})
	require.Empty(t, recorder.Ended())
	finish(context.DeadlineExceeded)
	startupSpan(t, recorder, "deadline_exceeded")
	require.Equal(t, base, WithBuildStartup(context.Background(), base))
}

func TestBuildStartupWithoutTracing(t *testing.T) {
	ctx, finish := TrackBuildStartup(context.Background(), time.Now())
	SetBuildStartupDriver(ctx, "kubernetes")
	now := time.Now()
	WithBuildStartup(ctx, discardStartupWriter{}).Write(&client.SolveStatus{Vertexes: []*client.Vertex{{Started: &now}}})
	finish(nil)
}
