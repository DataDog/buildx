# Build startup latency and worker identity

`docker buildx build` emits one `buildx.build.startup` span per invocation that
enters the build handler. It starts at handler entry, before builder lookup, and
ends when the client first receives a solver vertex with a `Started` timestamp.
It uses the command's existing tracer provider and export configuration. No new
metric instrument or exporter is installed.

Multiple platforms share one observation. Cached vertices count as progress;
client-generated bootstrap/policy messages do not. Existing build spans retain
their command parent rather than becoming children of the short-lived span.

`buildx.startup.outcome` distinguishes:

- `first_progress`: the client received a started solver vertex.
- `error`, `canceled`, `deadline_exceeded`: termination before first progress.
- `completed_without_progress`: successful return without a started vertex.

`driver` is added once builder loading succeeds; early failures can lack it.
`buildx.version` identifies the client. CI/job/repository resource attributes
come from the existing CI OTel configuration in the delivery patch stack.
A later build failure does not change an already recorded startup outcome.

This measures client preparation and time to first solver progress, including
Dockerfile/context loading. It is not worker queue time, time to the first RUN,
or total build duration. It covers `build`, including frontend calls made with
`--call`; it does not cover `bake`, library callers, argument/plugin failures
before the handler, or the preceding runner `buildx create` commands. Runner
retries/backoff and builder setup need a separate measurement, not a duration
added to every build that reuses the builder.

## Builder setup duration

`buildx.builder.create` spans cover each `buildx create` invocation, including
store access, builder initialization and optional `--bootstrap`. Attributes are
`driver`, `buildx.version`, `buildx.create.append`, `buildx.create.bootstrap`,
`buildx.create.leave` and `buildx.create.outcome` (success/error/canceled/deadline_exceeded).
Some existing initialization errors stringify the underlying cause; those retain
the generic `error` outcome. Error details remain on the trace.

The runner template creates an amd64 node and appends arm64 without `--bootstrap`,
and may retry each command. That is two or more create spans per CI job, not one
create per image build. External retry sleeps and cleanup between attempts are
outside the spans. Do not sum create duration into every subsequent build.
Exclude `@buildx.create.leave:true` when measuring creation. Graph create duration
and outcomes separately from build startup, optionally splitting append/bootstrap.

The existing CI OTel configuration now runs inside `TraceCurrentCommand`, before
resource/exporter creation. Build, Bake and Create therefore share endpoint
discovery and CI/job/repository attributes; explicit OTel settings are preserved.

## Datadog notebook and SLO configuration

After porting and releasing this patch through ci-delivery, first verify spans
from real CI jobs using:

```text
service:buildx resource_name:buildx.build.startup @ci.job.id:*
```

Do not require `driver:kubernetes` when counting initialization failures: the
driver may not yet be known. Scope all queries to the same supported CI population.

Generate the following span-based metrics (proposed names):

| Metric | Aggregation | Additional filter |
| --- | --- | --- |
| `ci.buildx.startup.attempts` | Count `*` | none |
| `ci.buildx.startup.duration` | Distribution `@duration` | `@buildx.startup.outcome:first_progress` |

Group attempts by `buildx.startup.outcome` (metric tag `outcome`); optionally group
both by repository and client version. Do not group by job ID, trace ID or pod.
Check the distribution's units: APM duration is in nanoseconds. Plot successful
startup p50/p95/p99 and counts by outcome in the notebook. These are new metrics,
not the old Buildx histogram from earlier revisions of this PR.

Choose a startup threshold **T** from observed data before enabling an SLO.
Then generate two COUNT metrics using the same base filter above:

- Good: `@buildx.startup.outcome:first_progress duration:<=T`
- Bad: `(@buildx.startup.outcome:first_progress duration:>T) OR @buildx.startup.outcome:(error OR canceled OR deadline_exceeded)`

Replace T with a Datadog duration literal, such as `10s`; this is an example,
not a selected target. Use the good and bad counts in a metric-based SLO:
`100 * good / (good + bad)`. Exclude `completed_without_progress` from both;
graph it separately as a coverage diagnostic. A quick initialization failure is
bad even when its duration is below T. Canceled attempts are bad under this
policy. Errors may include user configuration errors; this is startup experience,
not an infrastructure-only failure classifier. Select the objective percentage
and time window separately (for example 99.9% over 30 days).

Span-based metrics use ingested spans, while ordinary notebook APM queries use
indexed spans. Verify ingestion/filtering and export coverage before presenting
the SLO as fleet-wide. Abruptly killed clients and permanently hung commands may
never export a completed startup span. A non-reporting client is not a successful
start. No Buildx-only patch can guarantee observation after SIGKILL; runner-side
completion evidence would be needed to account for those attempts.

References: [span-based metrics](https://docs.datadoghq.com/tracing/trace_pipeline/generate_metrics/),
[metric SLOs](https://docs.datadoghq.com/service_level_objectives/metric/).

## Worker identity and local validation

The Kubernetes driver logs `Connecting to Kubernetes BuildKit worker` with
namespace, pod name/UID, node and container before opening the exec connection.
It identifies an attempt, not proof that a solve started or that the worker
caused a failure. These identifiers are not metric dimensions.

With a localhost OTLP/gRPC collector, both plugin and standalone clients use the
existing trace exporter:

```sh
OTEL_TRACES_EXPORTER=otlp OTEL_EXPORTER_OTLP_PROTOCOL=grpc \
  OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4317 \
  docker buildx build --builder your-local-builder --progress=plain .
```

A nonexistent builder must export one startup span with outcome `error` before
the command tracer shuts down. A normal or cached solve must end startup at first
progress, before the command completes. Multiple platforms must not duplicate it.
Test with `go test ./util/progress ./commands ./build ./driver/kubernetes/...`.

This PR targets `v0.38.2-dd`, including existing CI tracing and worker-reselection
patches. Startup finishes before command export shutdown; worker identity is
logged inside each connection attempt. After review, port the net change through
ci-delivery. No release or deployment is performed by this PR.
