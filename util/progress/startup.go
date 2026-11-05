package progress

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/docker/buildx/util/metricutil"
	"github.com/moby/buildkit/client"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type startupKey struct{}

type startupMeasurement struct {
	once     sync.Once
	start    time.Time
	duration metric.Float64Histogram
}

// TrackBuildStartup measures command entry to the first started solver vertex.
// finish must be called even if initialization fails before a solver is created.
func TrackBuildStartup(ctx context.Context, mp metric.MeterProvider) (context.Context, func(error)) {
	start := time.Now()
	h, err := metricutil.Meter(mp).Float64Histogram("buildx.build.startup.duration",
		metric.WithDescription("Time from build command entry to first started solver vertex, or termination before progress"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(.01, .05, .1, .5, 1, 2, 5, 10, 30, 60, 120, 300, 600))
	if err != nil {
		// Instrumentation must never prevent a build.
		return ctx, func(error) {}
	}
	m := &startupMeasurement{start: start, duration: h}
	return context.WithValue(ctx, startupKey{}, m), func(err error) {
		outcome := "completed_without_progress"
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			outcome = "deadline_exceeded"
		case errors.Is(err, context.Canceled):
			outcome = "canceled"
		case err != nil:
			outcome = "error"
		}
		m.record(outcome)
	}
}

func (m *startupMeasurement) record(outcome string) {
	m.once.Do(func() {
		// Keep canceled attempts observable, independently of the build context.
		m.duration.Record(context.Background(), time.Since(m.start).Seconds(),
			metric.WithAttributes(attribute.String("outcome", outcome)))
	})
}

type startupWriter struct {
	Writer
	m *startupMeasurement
}

// WithBuildStartup observes only a solver's status stream. Do not wrap the main
// printer: it also receives synthetic client-side bootstrap and policy progress.
func WithBuildStartup(ctx context.Context, w Writer) Writer {
	m, _ := ctx.Value(startupKey{}).(*startupMeasurement)
	if m == nil {
		return w
	}
	return &startupWriter{Writer: w, m: m}
}

func (w *startupWriter) Write(s *client.SolveStatus) {
	for _, v := range s.Vertexes {
		if v.Started != nil {
			w.m.record("first_progress")
			break
		}
	}
	w.Writer.Write(s)
}
