# Local reproduction: Unready worker selection and initialization deadlines

## Reproduced

Both baseline choosers selected the only pod despite Ready=False. The safety assertion failed 5/5 for random and sticky selection; the chooser implementation from PR #10, commit 3a7de271ac881d0e77038d78a0bc751ce1aec192, passed 5/5 through a Go overlay.

    BUILDX_LOCAL_REPRO=1 go test -mod=vendor ./driver/kubernetes/podchooser -run TestLocalReproRejectUnreadyWorker -count=5 -v

The separate socket fixture accepts connections without speaking gRPC. Buildx create/bootstrap with --timeout=2s returned context deadline exceeded after about 40.1 seconds. The option describes loading builder status; trace bootstrap separately before claiming the flag should bound that phase.

## Experiment

Use fake pod objects for deterministic selection. For initialization, record the accepted connection, exact command, duration and deadline output. The 75-second watchdog is a harness failure. A recoverable setup error must remain distinct from a final build failure.

Next extend readiness coverage to missing/Unknown conditions and real transitions only if needed. Running does not imply Ready. No cluster was required for these results.

## Run locally

Prerequisites: Linux, Python 3, a C compiler with static libc support, local Docker, and the pinned BuildKit image already cached. The runner never pulls an image. It creates a disposable privileged BuildKit container limited to 2 CPUs and 768 MiB including swap, with a private Unix socket and Buildx configuration. Registry fixtures bind only to loopback port 51271; run scenarios sequentially.

    export LAB_ARTIFACTS=/var/tmp/buildx-local
    mkdir -p "$LAB_ARTIFACTS"
    python3 tests/repro/local/run.py case_initialization \
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

## Follow-up version comparison (October 27)

Latest upstream Buildx v0.38.1 was also tested with the same pinned daemon. The unresponsive bootstrap endpoint still returned after approximately 40 seconds with --timeout=2s. This flag describes loading builder status; this observation alone is not a timeout-contract defect.

The runner now accepts --buildx-binary /absolute/path/to/buildx. It selects that binary through a private Docker plugin directory and records its SHA-256; it does not replace the installed plugin. Latest stable was verified through the upstream GitHub releases API as v0.38.1 (October 20, 2026). The tested arm64 release checksum is 3b83b3731916ba433feccaaa689fe5bcdc81c641d92c8e6c2ea865dfa141c97a. Daemon version remains v0.33.1; a client upgrade does not upgrade a remote daemon.
