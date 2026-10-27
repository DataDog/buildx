# Local reproduction: Registry stalls and the pusher Commit deadline

## Reproduced dependency defect

Vendored containerd Commit ignored its own 100 ms context deadline while the original Push context remained live and the registry withheld its response: 5/5 failing safety tests, with healthy controls passing. Cancelling the original Push context unblocked Commit.

A local-only overlay adding a ctx.Done branch in Commit's response select passed 5/5. This is not committed as a daemon fix: map the deployed dependency and calling contexts separately.

    BUILDX_LOCAL_REPRO=1 go test -mod=vendor ./tests/repro/push -run TestLocalReproCommitDeadline -count=5 -v

This Go test uses an in-process loopback HTTP server and needs no Docker.

## End-to-end experiment and negative result

The Docker scenario withholds upload responses. A real push stayed blocked for ten seconds without a terminal error. All eight cancellations exited promptly (130); a new push succeeded before old server handlers were released. A cancellation-resistant end-to-end hang and leaked push capacity were NOT reproduced.

Require a healthy control, prove an upload reached the fault barrier, retain the first stall for ten seconds, cancel eight pushes, and require a successful new push before releasing old handlers. The source notebook's inference that cancellation never unblocks the writer is too broad: Commit's independently scoped deadline differs from cancellation of Push. Ten seconds of induced silence is not a full GitLab timeout reproduction.

## Run locally

Prerequisites: Linux, Python 3, a C compiler with static libc support, local Docker, and the pinned BuildKit image already cached. The runner never pulls an image. It creates a disposable privileged BuildKit container limited to 2 CPUs and 768 MiB including swap, with a private Unix socket and Buildx configuration. Registry fixtures bind only to loopback port 51271; run scenarios sequentially.

    export LAB_ARTIFACTS=/var/tmp/buildx-local
    mkdir -p "$LAB_ARTIFACTS"
    python3 tests/repro/local/run.py case_push \
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
