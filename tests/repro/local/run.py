"""Run one explicitly selected reproduction against a disposable local worker."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import uuid

HERE = Path(__file__).resolve().parent
DOCKER = ["docker", "--host", "unix:///var/run/docker.sock"]
LABEL = "buildx-local-reproduction"
def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("scenario", choices=[p.stem for p in HERE.glob("case_*.py")])
    parser.add_argument("--image", required=True, help="Already-cached BuildKit image pinned with @sha256:")
    parser.add_argument("--artifacts", required=True, type=Path, help="Directory on a filesystem with free space")
    parser.add_argument("--solve-helper", type=Path, help="Locally built helper for case_deadline")
    args = parser.parse_args()
    if "@sha256:" not in args.image:
        parser.error("--image must be pinned by digest")
    args.artifacts.mkdir(parents=True, exist_ok=True)
    root = Path(tempfile.mkdtemp(prefix="r-", dir=args.artifacts.resolve()))
    if len(str(root / "socket" / "buildkitd.sock").encode()) > 107:
        parser.error("artifact path is too long for a Unix socket; choose a shorter path")
    for directory in ["context", "socket", "tmp", "evidence"]:
        (root / directory).mkdir()
    env = dict(os.environ)
    env.pop("DOCKER_CONTEXT", None)
    env.update(DOCKER_HOST="unix:///var/run/docker.sock",
               BUILDX_REPRO_ROOT=str(root), BUILDX_CONFIG=str(root / "buildx"),
               TMPDIR=str(root / "tmp"))
    if args.solve_helper:
        env["BUILDX_SOLVE_HELPER"] = str(args.solve_helper.resolve())
    name = "buildx-repro-" + uuid.uuid4().hex[:12]
    env["BUILDX_REPRO_CONTAINER"] = name
    def command(argv, **kwargs):
        return subprocess.run(argv, env=env, check=True, capture_output=True, text=True, **kwargs)
    container = None
    print("Artifacts: " + str(root), flush=True)
    try:
        # No image pulls; inspecting the image also verifies local Docker availability.
        image = json.loads(command(DOCKER + ["image", "inspect", args.image]).stdout)[0]
        shutil.copy2(HERE / "fixture.c", root / "context" / "repro.c")
        command(["cc", "-static", "-O0", "-o", str(root / "context" / "repro"), str(root / "context" / "repro.c")])
        result = command(DOCKER + [
            "run", "-d", "--pull=never", "--privileged", "--network=host",
            "--name", name, "--label", LABEL + "=true", "--memory=768m",
            "--memory-swap=768m", "--cpus=2",
            "--mount", "type=bind,src=" + str(root / "socket") + ",dst=/run/repro",
            args.image, "--addr", "unix:///run/repro/buildkitd.sock",
            "--group", str(os.getgid()), "--debug",
        ])
        container = result.stdout.strip()
        sock = root / "socket" / "buildkitd.sock"
        for _ in range(300):
            if sock.exists() and os.access(sock, os.R_OK | os.W_OK):
                break
            time.sleep(0.05)
        else:
            raise RuntimeError("disposable daemon did not create an accessible socket")
        command(DOCKER + ["cp", container + ":/usr/bin/buildctl", str(root / "buildctl")])
        command(DOCKER + ["buildx", "create", "--name", "local-repro", "--driver", "remote", "unix://" + str(sock), "--bootstrap"], timeout=30)
        manifest = {
            "image_reference": args.image, "image_id": image["Id"],
            "architecture": image["Architecture"], "container_id": container,
            "container_name": name,
            "buildx_version": command(DOCKER + ["buildx", "version"]).stdout.strip(),
            "buildctl_version": command([str(root / "buildctl"), "--version"]).stdout.strip(),
            "scenario": args.scenario, "started_at": time.time(),
        }
        (root / "evidence" / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
        result = subprocess.run([sys.executable, str(HERE / (args.scenario + ".py"))], env=env, timeout=300)
        return result.returncode
    finally:
        if container:
            # Only the ID created above is ever stopped/removed. Preserve state and logs first.
            state = subprocess.run(DOCKER + ["inspect", container], env=env, capture_output=True, text=True)
            (root / "evidence" / "container-final.json").write_text(state.stdout)
            logs = subprocess.run(DOCKER + ["logs", container], env=env, capture_output=True, text=True)
            (root / "evidence" / "daemon.log").write_text(logs.stdout + logs.stderr)
            subprocess.run(DOCKER + ["unpause", container], env=env, capture_output=True)
            subprocess.run(DOCKER + ["rm", "-f", "-v", container], env=env, check=True, capture_output=True)
        print("Evidence retained: " + str(root / "evidence"), flush=True)

if __name__ == "__main__":
    sys.exit(main())
