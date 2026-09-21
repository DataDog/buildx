# Kubernetes build connection recovery

The Kubernetes driver carries BuildKit traffic over Kubernetes exec streams.
Terminating an API-server replica can interrupt a running build even when the
BuildKit pod remains healthy.

`buildx build` restarts eligible Kubernetes builds at most twice when gRPC reports
`Unavailable` and the status message contains `error reading from server: EOF` or
`unexpected EOF`, including when gRPC wraps it (for example `closing transport due
to: connection error: desc = "error reading from server: EOF"`). It waits
one second before the first retry and two seconds before the second. Each retry
creates fresh connections and a new build session. Cancellation interrupts the
wait; ordinary Dockerfile errors and other failures are returned without retry.

Recovery requires explicit image, registry, or cache-only output (including
`--push`). It is disabled for debugger sessions, explicit solve references,
stdin inputs, nonregular local Dockerfiles or secret files, local cache exports,
local/file/stdout exports, and Docker image loading. This also avoids replaying
an implicit `default-load` output.

This restarts the build; it does not resume the disconnected session. Cached
steps can be reused, but uncached commands and registry publication may run
again. It does not guarantee exactly-once external side effects. Repeated
connection loss can exhaust the two-retry budget. The change applies to
`buildx build`; it does not add retry behavior to `buildx bake`.
