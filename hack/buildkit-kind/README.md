# Local Kind failure harness

Tracks [CIEXE-2715](https://datadoghq.atlassian.net/browse/CIEXE-2715).
This creates and removes a disposable cluster on the local Docker daemon. It never
uses your current kubeconfig, Docker plugin directory or an existing Kubernetes cluster.
It requires local Docker, Kind, kubectl, Python 3.11+, Go and a C compiler with static libc.

Build a client from the branch you want to exercise:

    go build -mod=vendor -o /path/with/space/buildx ./cmd/buildx

Cache the chosen node and BuildKit images first. Pin both references by digest.
Export only the host platform; a partially cached multi-platform image index cannot
be imported with Kind's all-platform import:

    docker image save --platform linux/arm64 --output /path/with/space/buildkit.tar moby/buildkit:buildx-stable-1

Run:

    python3 hack/buildkit-kind/run.py \
      --kind /path/to/kind \
      --node-image kindest/node:v1.35.8@sha256:07b2536e30b803ed61d1677a79df6115f798ce64c80f9e22f6ed45afd09323c0 \
      --buildkit-image moby/buildkit@sha256:cec9f139f45e93c5c69c60f8b07cfad9f43f4ef6b6a6cd917527fea5ff2e3dea \
      --archive-tag moby/buildkit:buildx-stable-1 \
      --image-archive /path/with/space/buildkit.tar \
      --buildx-binary /path/with/space/buildx \
      --artifacts /path/with/space/evidence

Use the platform matching your host for docker save. The tested pair is ARM64 Kind
v0.33.0 / Kubernetes v1.35.8 and BuildKit v0.33.1. The harness verifies imported
filesystem layers, platform and runtime config against the pinned Docker image before
using its archive tag. Registry index and container config hashes are different objects.

The fixture uses FROM scratch and a locally compiled static executable, so builds do
not need base-image downloads. Cases require healthy builds and expected failures:

1. A RUN emits stderr and exits 42; the failing instruction appears in client output.
2. A loopback TCP relay severs established API/exec streams during a running instruction.
   The build must fail, then a healthy build must succeed on the same worker UID.
3. Deleting the active worker interrupts a build; its replacement must build successfully.
4. Scaling the deployment to zero and back to one must recover.

The Kind node is capped at 4 GiB / 4 CPUs after creation; the worker at 768 MiB / 2 CPUs.
Expect initial cluster creation and image loading to require additional transient capacity.
The harness always attempts cleanup, including partial setup failures; hard process or
host termination can prevent cleanup. Cluster names and commands are recorded, so remove
only that named local cluster if interrupted.

Evidence contains command output, interrupted-build logs, final pod/event/daemon logs,
client checksum/version and boolean results. Kubeconfig files contain credentials for
the disposable local cluster: do not upload the whole artifact directory. Raw kubeconfig
output is omitted from commands.log. Upload sanitized logs/results only.

These are deterministic injected mechanisms, not proof of production root causes or
evidence that a particular client patch fixes all failure categories. Run the same
harness with a candidate binary and a control binary; keep versions and injected faults
in the comparison. Other reproduction PRs cover registry stalls, OOM, content errors
and unit-level Kubernetes setup/cleanup regressions.
