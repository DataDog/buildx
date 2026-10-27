# Local reproduction: Dockerfile failures, cancellation, and abrupt client kill

## Reproduced

The offline fixture prints execution/stderr markers and exits 42. Buildx identifies the RUN and exit code and returns nonzero. Cancellation returns 130; SIGKILL ends the client without a final summary. Healthy controls pass before and afterward.

These are diagnostic controls and termination mechanisms, not newly discovered BuildKit defects. The Buildx process need not return the Dockerfile process's exit code.

## Experiment

Compile a tiny static fixture and use FROM scratch, removing base-image downloads. Require the healthy control, then require a nonzero failure with exit code: 42 before the watchdog. Wait for actual execution before SIGTERM and SIGKILL in separate runs, retain the injected signal, and require a final healthy build.

The hard kill simulates runner termination; it is not an actual GitLab timeout. Nothing can emit new client diagnostics after SIGKILL, so retain progress beforehand. Follow-up cases should cover COPY/parse errors, multistage attribution and log truncation.

## Run locally

Prerequisites: Linux, Python 3, a C compiler with static libc support, local Docker, and the pinned BuildKit image already cached. The runner never pulls an image. It creates a disposable privileged BuildKit container limited to 2 CPUs and 768 MiB including swap, with a private Unix socket and Buildx configuration. Registry fixtures bind only to loopback port 51271; run scenarios sequentially.

    export LAB_ARTIFACTS=/var/tmp/buildx-local
    mkdir -p "$LAB_ARTIFACTS"
    python3 tests/repro/local/run.py case_instruction \
      --image moby/buildkit@sha256:cec9f139f45e93c5c69c60f8b07cfad9f43f4ef6b6a6cd917527fea5ff2e3dea \
      --artifacts "$LAB_ARTIFACTS"

The runner explicitly uses unix:///var/run/docker.sock and removes only its newly created container by ID. It preserves logs, timing/exit-status JSON, image/version identity and final container state. It does not change the default builder, contact Kubernetes, edit CI jobs, publish an image or deploy anything. Host networking supports loopback fixtures; the BuildKit API remains on a private Unix socket. OOM injection requires verification of the instruction's actual memory cap.

## Tested scope

Initial experiments used Linux arm64, installed Buildx v0.34.1 (e0b0e77d18d3379bc1e0d55f3b37de288d36fe47), and cached upstream BuildKit v0.33.1 (8c91502cf280bd70a0c50912ce251c46a8881d9f), pinned above. This does not establish equivalence with the internally patched fleet image. Each run records the selected versions.

Go baseline: afb23fb5bdd5ec32eb6d2225c2a0f813ecac174d. Opt-in safety tests deliberately fail on affected code and skip unless BUILDX_LOCAL_REPRO=1. These are manual reproductions, not new CI jobs. Watchdog termination is a failed/inconclusive experiment, not the intended error.

[Progress notebook](https://app.datadoghq.com/notebook/16090014) · [Central failure catalogue](https://app.datadoghq.com/notebook/16073186)

## Review and next steps

Keep this PR in draft until the mechanism and assertions are reviewed. Compare baseline and candidate while changing one component at a time, and retain negative results. An injected signature does not prove production causality or frequency.

Any eventual client patch must follow the Compute team's reviewed port into ddoghq/compute-delivery/repos/buildx. Do not merge this fork expecting a CI rollout. Daemon/dependency patches require their authoritative packaging path. No release or deployment is included.

Shared helper files are identical across these independent reproduction drafts so each branch runs from master without depending on another experiment.
