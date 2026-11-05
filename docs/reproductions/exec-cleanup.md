# Close the Kubernetes exec transport with its connection

## Exact scenario

A loopback HTTP server performs the real Kubernetes SPDY upgrade, creates the four exec streams, and sends READY on stdout. In the defect case, the client calls Close and the server must observe transport closure within one second; the parent build context must remain usable. The negative control closes the server transport and verifies that the blocked client read returns an error. Additional executor tests cover repeated Close, cancellation, and stream completion.

## Repeat the comparison locally

Run from this PR branch. Requires Go with the repository's vendored dependencies,
Python 3, Git history containing `afb23fb5bdd5ec32eb6d2225c2a0f813ecac174d`, and local loopback sockets.
No cluster, registry credentials or deployment is required. Use a filesystem with
space for Go's temporary files and cache.

```sh
export ARTIFACTS="$(mktemp -d)"
python3 - <<'PYTHON'
from pathlib import Path
import json
import os
import subprocess

repo = Path.cwd().resolve()
out = Path(os.environ["ARTIFACTS"]).resolve()
base = "afb23fb5bdd5ec32eb6d2225c2a0f813ecac174d"
replace = {}
for i, name in enumerate(["driver/kubernetes/execconn/execconn.go"]):
    p = out / f"baseline-{i}.go"
    p.write_bytes(subprocess.check_output(["git", "show", f"{base}:{name}"] ))
    replace[str(repo / name)] = str(p)
(out / "overlay.json").write_text(json.dumps({"Replace": replace}))
PYTHON
# Expected failure: confirm the specific assertion below, not a compile/network error.
go test -mod=vendor -overlay="$ARTIFACTS/overlay.json" -v -count=3 \
  -timeout=120s ./driver/kubernetes/execconn -run 'TestExecConnectionSPDYLifecycle'
# Expected success: same regression and fixtures with the patch, uncached.
go test -mod=vendor -v -count=10 -timeout=120s \
  ./driver/kubernetes/execconn -run 'TestExecConnectionSPDYLifecycle'
go test -mod=vendor -race -count=1 -timeout=180s ./driver/kubernetes/...
go build -mod=vendor -o "$ARTIFACTS/buildx" ./cmd/buildx
"$ARTIFACTS/buildx" version
sha256sum "$ARTIFACTS/buildx"
```

The overlay replaces the production files changed by this PR with their pre-fix
versions while keeping the regression test and the same dependency environment.
It does not switch your branch.

The expected baseline assertion is:

```text
client Close left real SPDY transport open
```

## Captured evidence

[Local comparison logs, 2026-11-05](evidence/exec-cleanup-20261105.log)
record the baseline revision, candidate production revision, commands, expected
failure and passing checks. Workspace paths are normalized; diagnostic messages
are preserved. These logs are a dated experiment, not a continuously updated CI result.

## Fresh binary smoke test

A new local binary built from production revision `3eabf80689538a98c731fb7a49444fb45209ab77` passed all seven
assertions in the [Kind harness](https://github.com/DataDog/buildx/blob/147d59f323b816d5605d95e522a6f83a466004c0/hack/buildkit-kind/README.md): healthy build, expected instruction failure, transport-cut failure, recovery on the same worker UID, worker-deletion failure, replacement recovery, and scale-down/up recovery.

Environment: Linux ARM64, Go 1.27.3, Kind 0.33.0, Kubernetes 1.35.8, BuildKit 0.33.1.
The local binary identifies itself as `v0.0.0+unknown`; its SHA256 is
`90108e57fd98bc8fbfd409cf95ac53b6771db57da41a7a850e723ed20001a0f8`. This is not a released client.
The dated evidence includes the injected failure logs and observed pod UIDs.
Each private test cluster was removed by the harness. These broad smoke cases do
not distinguish patched from unpatched behavior; the focused baseline comparison
above does that. Mid-build transport cuts are expected to fail even with this fix.

## What this does not prove

This proves transport cleanup against a real local SPDY server, not that every production EOF or hang shares this cause. The server is a test endpoint, not a Kubernetes cluster or BuildKit daemon. A server-initiated disconnect still produces EOF and can log "io: read/write on closed pipe" on the patched client; those messages alone are not evidence of a cleanup regression.
