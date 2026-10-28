# Local reproduction: Solve deadlines, worker probes, and bounded CPU pressure

## Reproduced

A running solve with a two-second context returned gRPC DeadlineExceeded at about 2.06 seconds; healthy controls passed before/after. Use BuildKit's gRPC error decoder: errors.Is against context.DeadlineExceeded alone was false for the wrapped remote error.

The worker probe (buildctl debug workers) exceeded five seconds while the disposable daemon was paused and passed before/after. This proves the unresponsive-worker mechanism, not natural saturation.

## Build the helper first

    export LAB_ARTIFACTS=/var/tmp/buildx-local
    mkdir -p "$LAB_ARTIFACTS"
    go build -mod=vendor -o "$LAB_ARTIFACTS/solve-deadline" ./tests/repro/deadline

The helper requires an actual execution marker before validating the remote deadline code. Its successful exit means the expected outcome was observed, not that the deadline solve succeeded. The probe scenario unpauses in a finally block.

## Resource-pressure experiment

Use case_pressure instead of case_deadline in the runner command. It starts four CPU-bound builds under a 0.1-CPU quota, measures six probes with a five-second budget, and restores two CPUs before cleanup.

Negative result: all four builds and six probes succeeded in the initial run (probe latency approximately 0.01–1.73 seconds). Natural overload/probe failure has not been reproduced. A paused-worker timeout is not evidence that overload caused a fleet restart; test settings are not tuning recommendations.

## Run locally

Prerequisites: Linux, Python 3, a C compiler with static libc support, local Docker, and the pinned BuildKit image already cached. The runner never pulls an image. It creates a disposable privileged BuildKit container limited to 2 CPUs and 768 MiB including swap, with a private Unix socket and Buildx configuration. Registry fixtures bind only to loopback port 51271; run scenarios sequentially.

    export LAB_ARTIFACTS=/var/tmp/buildx-local
    mkdir -p "$LAB_ARTIFACTS"
    python3 tests/repro/local/run.py case_deadline \
      --image moby/buildkit@sha256:cec9f139f45e93c5c69c60f8b07cfad9f43f4ef6b6a6cd917527fea5ff2e3dea \
      --artifacts "$LAB_ARTIFACTS" \
      --solve-helper "$LAB_ARTIFACTS/solve-deadline"

The runner explicitly uses unix:///var/run/docker.sock and removes only its newly created container by ID. It preserves logs, timing/exit-status JSON, image/version identity and final container state. It does not change the default builder, contact Kubernetes, edit CI jobs, publish an image or deploy anything. Host networking supports loopback fixtures; the BuildKit API remains on a private Unix socket. OOM injection requires verification of the instruction's actual memory cap.

## Tested scope

Initial experiments used Linux arm64, installed Buildx v0.34.1 (e0b0e77d18d3379bc1e0d55f3b37de288d36fe47), and cached upstream BuildKit v0.33.1 (8c91502cf280bd70a0c50912ce251c46a8881d9f), pinned above. This does not establish equivalence with the internally patched fleet image. Each run records the selected versions.

Go baseline: afb23fb5bdd5ec32eb6d2225c2a0f813ecac174d. Opt-in safety tests deliberately fail on affected code and skip unless BUILDX_LOCAL_REPRO=1. These are manual reproductions, not new CI jobs. Watchdog termination is a failed/inconclusive experiment, not the intended error.

[Progress notebook](https://app.datadoghq.com/notebook/16090014) · [Central failure catalogue](https://app.datadoghq.com/notebook/16073186)

## Review and next steps

Keep this PR in draft until the mechanism and assertions are reviewed. Compare baseline and candidate while changing one component at a time, and retain negative results. An injected signature does not prove production causality or frequency.

Any eventual client patch must follow the Compute team's reviewed port into ddoghq/compute-delivery/repos/buildx. Do not merge this fork expecting a CI rollout. Daemon/dependency patches require their authoritative packaging path. No release or deployment is included.

Shared helper files are identical across these independent reproduction drafts so each branch runs from master without depending on another experiment.

## Follow-up version comparison (October 27)

A follow-up experiment at 0.01 CPU, including a quiet probe control at the same quota, still passed all six loaded probes and all four builds. Natural pressure-induced probe failure remains unproven. Set BUILDX_REPRO_CPU_LIMIT only for this disposable scenario (accepted range 0.01 to 2); these limits are not fleet recommendations.

The runner now accepts --buildx-binary /absolute/path/to/buildx. It selects that binary through a private Docker plugin directory and records its SHA-256; it does not replace the installed plugin. Latest stable was verified through the upstream GitHub releases API as v0.38.1 (October 20, 2026). The tested arm64 release checksum is 3b83b3731916ba433feccaaa689fe5bcdc81c641d92c8e6c2ea865dfa141c97a. Daemon version remains v0.33.1; a client upgrade does not upgrade a remote daemon.
