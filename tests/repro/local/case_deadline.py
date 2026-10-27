import json
import os
from pathlib import Path
import subprocess
import time
import repro

helper = Path(os.environ["BUILDX_SOLVE_HELPER"])
results = []
for label, mode in [("healthy", "ok"), ("solve-deadline", "sleep"), ("recovery", "ok")]:
    command = [str(helper), "-addr", "unix://" + repro.SOCK, "-fixture", str(repro.CTX / "repro"), "-mode", mode]
    p = subprocess.run(command, env=repro.ENV, capture_output=True, text=True, timeout=15)
    (repro.OUT / (label + ".log")).write_text(p.stdout + p.stderr)
    assert p.returncode == 0, p.stdout + p.stderr
    results.append(json.loads(p.stdout))
assert results[1]["deadline_exceeded"] and results[1]["started"]

probe = [str(repro.ROOT / "buildctl"), "--addr", "unix://" + repro.SOCK, "debug", "workers"]
probes = []
for pause in [False, True, False]:
    if pause:
        subprocess.run(["docker", "pause", os.environ["BUILDX_REPRO_CONTAINER"]], env=repro.ENV, check=True, capture_output=True)
    start = time.monotonic()
    try:
        p = subprocess.run(probe, env=repro.ENV, capture_output=True, text=True, timeout=5)
        result = {"paused": pause, "timeout": False, "returncode": p.returncode}
    except subprocess.TimeoutExpired:
        result = {"paused": pause, "timeout": True}
    finally:
        if pause:
            subprocess.run(["docker", "unpause", os.environ["BUILDX_REPRO_CONTAINER"]], env=repro.ENV, check=True, capture_output=True)
    result["elapsed_seconds"] = time.monotonic() - start
    probes.append(result)
(repro.OUT / "probe-timeout.json").write_text(json.dumps(probes, indent=2))
assert not probes[0]["timeout"] and probes[0]["returncode"] == 0
assert probes[1]["timeout"]
assert not probes[2]["timeout"] and probes[2]["returncode"] == 0
print(json.dumps({"solves": results, "probes": probes}), flush=True)
