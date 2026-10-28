import json
import os
from pathlib import Path
import subprocess
import time
import repro

NAME = os.environ["BUILDX_REPRO_CONTAINER"]
def state():
    return json.loads(subprocess.check_output(["docker", "inspect", NAME], env=repro.ENV, text=True))[0]["State"]
def ready():
    for _ in range(20):
        try:
            p = subprocess.run([str(repro.ROOT / "buildctl"), "--addr", "unix://" + repro.SOCK, "debug", "workers"],
                               env=repro.ENV, capture_output=True, timeout=1)
            if p.returncode == 0:
                return
        except subprocess.TimeoutExpired:
            pass
        time.sleep(0.1)
    raise RuntimeError("disposable worker did not become ready")
def restart():
    for _ in range(100):
        if not state()["Running"]:
            break
        time.sleep(0.1)
    else:
        raise RuntimeError("test worker did not exit")
    subprocess.run(["docker", "start", NAME], env=repro.ENV, check=True, capture_output=True)
    ready()

assert repro.run_case("healthy")["returncode"] == 0
for label, mode, command in [
    ("worker-graceful-drain", "drain", ["docker", "kill", "--signal", "TERM", NAME]),
    ("worker-grace-expired", "sleep", ["docker", "stop", "--time", "1", NAME]),
    ("worker-sigkill", "sleep", ["docker", "kill", "--signal", "KILL", NAME]),
]:
    def inject(process):
        subprocess.run(command, env=repro.ENV, check=True, capture_output=True, timeout=20)
    result = repro.run_case(label, mode, action=inject, limit=15)
    assert result["marker_observed"] and not result["watchdog_killed"]
    assert (result["returncode"] == 0) == (label == "worker-graceful-drain")
    for _ in range(100):
        if not state()["Running"]:
            break
        time.sleep(0.1)
    (repro.OUT / (label + "-state.json")).write_text(json.dumps(state(), indent=2))
    restart()
    assert repro.run_case(label + "-recovery")["returncode"] == 0

# Verify the actual instruction cgroup before enabling a bounded OOM fixture.
memory_events = None
def inspect_memory(process):
    global memory_events
    output = subprocess.check_output(["docker", "top", NAME, "-eo", "pid,comm"], env=repro.ENV, text=True)
    records = []
    try:
        for line in output.splitlines()[1:]:
            pid, comm = line.split(None, 1)
            if comm.strip() != "repro":
                continue
            cg = Path("/proc", pid, "cgroup").read_text().strip()
            parent = Path("/sys/fs/cgroup") / cg.split("::", 1)[1].lstrip("/")
            while parent != Path("/sys/fs/cgroup"):
                maximum = parent / "memory.max"
                if maximum.exists():
                    value = maximum.read_text().strip()
                    records.append({"path": str(parent), "memory_max": value})
                    if value.isdigit() and int(value) <= 768 * 1024 * 1024:
                        memory_events = parent / "memory.events"
                parent = parent.parent
        (repro.OUT / "memory-isolation.json").write_text(json.dumps(records, indent=2))
    finally:
        process.terminate()
repro.run_case("verify-memory-isolation", "sleep", action=inspect_memory, limit=10)
if memory_events is None:
    raise RuntimeError("could not verify instruction's cgroup limit; OOM experiment not run")
def events():
    return {k: int(v) for k, v in (line.split() for line in memory_events.read_text().splitlines())}
before = events()
result = repro.run_case("memory-exhaustion", "memory", limit=40)
after = events()
(repro.OUT / "oom-evidence.json").write_text(json.dumps({"before": before, "after": after, "state": state(), "result": result}, indent=2))
assert after["oom_kill"] > before["oom_kill"] and result["returncode"] != 0
assert not result["watchdog_killed"]
if not state()["Running"]:
    restart()
assert repro.run_case("healthy-after-oom")["returncode"] == 0
