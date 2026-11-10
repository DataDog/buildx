package progress

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/docker/buildx/version"
	"github.com/moby/buildkit/client"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type startupKey struct{}

type startupMeasurement struct {
	once sync.Once
	span trace.Span
}

// TrackBuildStartup measures command entry to the first received started solver
// vertex. Call after TraceCurrentCommand; finish must run before its exporter is
// shut down, including when initialization fails before a solver is created.
func TrackBuildStartup(ctx context.Context, start time.Time) (context.Context, func(error)) {
	_, span := trace.SpanFromContext(ctx).TracerProvider().Tracer(version.Package).Start(ctx,
		"buildx.build.startup", trace.WithTimestamp(start), trace.WithAttributes(
			attribute.String("buildx.version", version.Version),
		))
	m := &startupMeasurement{span: span}
	// Keep the command span as parent of the actual build: startup ends early.
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

// SetBuildStartupDriver enriches the observation once the builder is known.
// Earlier failures remain observable without a driver attribute.
func SetBuildStartupDriver(ctx context.Context, driver string) {
	if m, _ := ctx.Value(startupKey{}).(*startupMeasurement); m != nil {
		m.span.SetAttributes(attribute.String("driver", driver))
	}
}

func (m *startupMeasurement) record(outcome string) {
	m.once.Do(func() {
		m.span.SetAttributes(attribute.String("buildx.startup.outcome", outcome))
		if outcome == "error" || outcome == "deadline_exceeded" || outcome == "canceled" {
			m.span.SetStatus(codes.Error, outcome)
		}
		m.span.End()
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
