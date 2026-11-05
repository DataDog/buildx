# Build startup latency

`docker buildx build` records `buildx.build.startup.duration`, an OpenTelemetry
histogram in seconds, once per command invocation that enters the build handler.
It starts before builder lookup/loading and stops when a solver status stream
first reports a vertex with a `Started` timestamp. Multiple platforms share one
observation. Client-generated bootstrap/policy messages do not stop the timer.

The bounded `outcome` attribute distinguishes:

- `first_progress`: a solver vertex started (including a cache hit).
- `error`, `canceled`, `deadline_exceeded`: the command terminated before progress.
- `completed_without_progress`: the command returned successfully without a
  started solver vertex, for example a path that does not execute a solve.

Use `outcome:first_progress` for startup latency percentiles. Graph the counts
of the other outcomes separately; they are time spent before termination, not
successful starts. A later build failure does not change its startup observation.
A killed client cannot emit its final observation. Argument parsing and Docker
plugin startup failures before the build handler are outside this measurement.

This is a startup proxy, not worker queue time or time to the first Dockerfile
`RUN`. It includes client preparation and connecting to the builder; the first
vertex can be Dockerfile/context loading. It covers `build`, not `bake` or
library callers of `RunBuild`. Worker concurrency waits require server metrics.

## Collection and validation

The metric uses the Docker CLI's existing meter provider and exporter. For a
local OTLP/gRPC collector listening on port 4317, run the built client as a
Docker CLI plugin:

```sh
DOCKER_CLI_OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4317 \
  docker buildx build --builder your-local-builder --progress=plain .
```

Configure the endpoint in the CI environment through the normal telemetry
configuration before expecting data; adding the instrument does not configure a
collector. Resource attributes such as cluster/architecture should be attached
by the telemetry pipeline. No job IDs, image names, or Dockerfile text are added
as metric attributes. Existing resource attributes are unchanged.

Local validation:

```sh
go test ./util/progress -run TestBuildStartup -count=1
go build -o ./bin/docker-buildx ./cmd/buildx
```

Test the failure path with a nonexistent builder and confirm one histogram
sample with `outcome=error`, despite the command returning a failure. Run through
`docker buildx` for this check: the current standalone CLI path does not enable
the Docker CLI meter provider.

For delivery, review this fork PR first, then port the approved patch to
`ddoghq/ci-delivery/repos/buildx/patches` and follow the normal release and image
rollout process. This change does not release a client or update CI images.
