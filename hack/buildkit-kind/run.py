"""Exercise Buildx against a disposable, private local Kind cluster."""

import argparse
import contextlib
import hashlib
import json
import os
from pathlib import Path
import select
import shutil
import socket
import subprocess
import threading
import time
from urllib.parse import urlparse
import uuid


class CuttableProxy:
    """Loopback-only TCP relay; cut active streams without killing the worker."""

    def __init__(self, upstream):
        self.upstream = upstream
        self.listener = socket.socket()
        self.listener.bind(("127.0.0.1", 0))
        self.listener.listen()
        self.listener.settimeout(0.2)
        self.port = self.listener.getsockname()[1]
        self.lock = threading.Lock()
        self.streams = set()
        self.stopped = threading.Event()
        self.thread = threading.Thread(target=self.accept, daemon=True)
        self.thread.start()

    def accept(self):
        while not self.stopped.is_set():
            try:
                client, _ = self.listener.accept()
            except TimeoutError:
                continue
            except OSError:
                return
            threading.Thread(target=self.relay, args=(client,), daemon=True).start()

    def relay(self, client):
        upstream = None
        try:
            upstream = socket.create_connection(self.upstream, timeout=5)
            upstream.settimeout(None)
            with self.lock:
                self.streams.update((client, upstream))
            while not self.stopped.is_set():
                ready, _, _ = select.select([client, upstream], [], [], 0.2)
                for source in ready:
                    data = source.recv(65536)
                    if not data:
                        return
                    (upstream if source is client else client).sendall(data)
        except (OSError, ValueError):
            pass
        finally:
            with self.lock:
                self.streams.discard(client)
                self.streams.discard(upstream)
            client.close()
            if upstream:
                upstream.close()

    def cut(self):
        with self.lock:
            streams = list(self.streams)
        for stream in streams:
            with contextlib.suppress(OSError):
                stream.shutdown(socket.SHUT_RDWR)
            stream.close()

    def close(self):
        self.stopped.set()
        self.listener.close()
        self.cut()
        self.thread.join(timeout=2)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--kind", required=True)
    parser.add_argument("--node-image", required=True, help="Cached image pinned with @sha256:")
    parser.add_argument("--buildkit-image", required=True, help="Cached image pinned with @sha256:")
    parser.add_argument(
        "--image-archive",
        required=True,
        type=Path,
        help="Host-platform docker save archive of the pinned BuildKit image",
    )
    parser.add_argument(
        "--archive-tag",
        required=True,
        help="Tag present in the archive; layers and config checked against pinned image",
    )
    parser.add_argument("--buildx-binary", required=True, type=Path)
    parser.add_argument("--artifacts", required=True, type=Path)
    args = parser.parse_args()
    for ref in [args.node_image, args.buildkit_image]:
        if "@sha256:" not in ref:
            parser.error("Images must be pinned by digest")
    root = args.artifacts.resolve() / ("kind-" + uuid.uuid4().hex[:12])
    root.mkdir(parents=True)
    name = "buildkit-repro-" + uuid.uuid4().hex[:12]
    kubeconfig = root / "kubeconfig"
    env = dict(
        os.environ,
        KUBECONFIG=str(kubeconfig),
        DOCKER_HOST="unix:///var/run/docker.sock",
        DOCKER_CONFIG=str(root / "docker"),
        BUILDX_CONFIG=str(root / "buildx"),
        TMPDIR=str(root),
    )
    env.pop("DOCKER_CONTEXT", None)
    plugins = root / "docker" / "cli-plugins"
    plugins.mkdir(parents=True)
    binary = args.buildx_binary.resolve(strict=True)
    (plugins / "docker-buildx").symlink_to(binary)
    docker = ["docker", "--host", "unix:///var/run/docker.sock"]

    def run(argv, *, check=True, timeout=120):
        result = subprocess.run(argv, env=env, text=True, capture_output=True, timeout=timeout, check=False)
        with (root / "commands.log").open("a") as log:
            log.write(
                json.dumps(argv)
                + "\n"
                + (
                    "[private kubeconfig omitted]\n"
                    if "config" in argv and "--raw" in argv
                    else result.stdout + result.stderr
                )
            )
        if check and result.returncode:
            raise RuntimeError(f"Command failed ({result.returncode}): {argv}; see {root}")
        return result

    kubectl = ["kubectl", "--kubeconfig", str(kubeconfig), "--context", "kind-" + name, "-n", "buildkit-repro"]

    def build(mode, *, check=True):
        (context / "Dockerfile").write_text('FROM scratch\nCOPY repro /repro\nRUN ["/repro", "' + mode + '"]\n')
        return run(
            [*docker, "buildx", "build", "--builder", "local-kind", "--progress=plain", "--no-cache", str(context)],
            check=check,
        )

    created = False
    child = None
    proxy = None
    deployment = None
    results = {}
    context = root / "context"
    context.mkdir()
    shutil.copyfile(Path(__file__).with_name("fixture.c"), context / "fixture.c")
    try:
        for ref in [args.node_image, args.buildkit_image]:
            run([*docker, "image", "inspect", ref])
        run(["cc", "-static", "-O0", "-o", str(context / "repro"), str(context / "fixture.c")])
        created = True  # Also clean up a partially created cluster.
        run(
            [
                args.kind,
                "create",
                "cluster",
                "--name",
                name,
                "--image",
                args.node_image,
                "--kubeconfig",
                str(kubeconfig),
                "--wait",
                "120s",
            ],
            timeout=240,
        )
        run([*docker, "update", "--memory=4g", "--memory-swap=4g", "--cpus=4", name + "-control-plane"])
        run([args.kind, "load", "image-archive", str(args.image_archive.resolve()), "--name", name])
        # Verify archive contents against the pinned image before using its imported tag.
        inspected = json.loads(run([*docker, "image", "inspect", args.buildkit_image]).stdout)[0]
        image_id = inspected["Id"]
        imported = json.loads(
            run([*docker, "exec", name + "-control-plane", "crictl", "inspecti", args.archive_tag]).stdout
        )
        spec = imported["info"]["imageSpec"]
        assert spec["rootfs"]["diff_ids"] == inspected["RootFS"]["Layers"], "Archive layers differ from pinned image"
        assert spec["architecture"] == inspected["Architecture"]

        def normalize(config):
            return {key: value for key, value in config.items() if value not in (None, "", [], {})}

        assert normalize(spec["config"]) == normalize(inspected["Config"]), (
            "Archive configuration differs from pinned image"
        )
        run(
            [
                "kubectl",
                "--kubeconfig",
                str(kubeconfig),
                "--context",
                "kind-" + name,
                "create",
                "namespace",
                "buildkit-repro",
            ]
        )
        run(
            [
                *docker,
                "buildx",
                "create",
                "--name",
                "local-kind",
                "--driver",
                "kubernetes",
                "--bootstrap",
                "--driver-opt",
                "namespace=buildkit-repro,image="
                + args.archive_tag
                + ",replicas=1,limits.memory=768Mi,limits.cpu=2,timeout=60s",
            ]
        )
        deployments = json.loads(run([*kubectl, "get", "deployments", "-o", "json"]).stdout)["items"]
        assert len(deployments) == 1
        deployment = deployments[0]["metadata"]["name"]
        # Keep kubectl on the direct private endpoint. Only Buildx traverses the relay.
        cfg = json.loads(
            run(["kubectl", "--kubeconfig", str(kubeconfig), "config", "view", "--raw", "-o", "json"]).stdout
        )
        assert len(cfg["clusters"]) == 1
        endpoint = urlparse(cfg["clusters"][0]["cluster"]["server"])
        assert endpoint.hostname == "127.0.0.1", "Refuse a nonlocal API endpoint"
        proxy = CuttableProxy((endpoint.hostname, endpoint.port))
        cfg["clusters"][0]["cluster"]["server"] = f"https://127.0.0.1:{proxy.port}"
        proxied_config = root / "proxied-kubeconfig"
        proxied_config.write_text(json.dumps(cfg))
        proxied_config.chmod(0o600)
        env["KUBECONFIG"] = str(proxied_config)
        # Recreate local builder metadata so its saved endpoint references the relay.
        run([*docker, "buildx", "rm", "--keep-daemon", "local-kind"])
        run(
            [
                *docker,
                "buildx",
                "create",
                "--name",
                "local-kind",
                "--driver",
                "kubernetes",
                "--bootstrap",
                "--driver-opt",
                "namespace=buildkit-repro,image="
                + args.archive_tag
                + ",replicas=1,limits.memory=768Mi,limits.cpu=2,timeout=60s",
            ]
        )
        results["healthy"] = build("ok").returncode == 0
        failed = build("fail", check=False)
        assert failed.returncode != 0 and "REPRO_STDERR" in failed.stderr and "42" in failed.stderr
        results["instruction_failure"] = True
        (context / "Dockerfile").write_text('FROM scratch\nCOPY repro /repro\nRUN ["/repro", "sleep"]\n')
        output = (root / "transport-cut.log").open("w")
        child = subprocess.Popen(
            [*docker, "buildx", "build", "--builder", "local-kind", "--progress=plain", "--no-cache", str(context)],
            env=env,
            stdout=output,
            stderr=subprocess.STDOUT,
        )
        for _ in range(120):
            if "REPRO_PROGRESS" in (root / "transport-cut.log").read_text():
                break
            if child.poll() is not None:
                raise RuntimeError("Build ended before disruption")
            time.sleep(0.25)
        else:
            raise RuntimeError("Build did not start within 30 seconds")
        pods = json.loads(run([*kubectl, "get", "pods", "-o", "json"]).stdout)["items"]
        assert len(pods) == 1
        uid = pods[0]["metadata"]["uid"]
        proxy.cut()
        code = child.wait(timeout=60)
        output.close()
        assert code != 0
        transport_log = (root / "transport-cut.log").read_text()
        assert "EOF" in transport_log or "closed pipe" in transport_log or "Unavailable" in transport_log
        results["transport_cut_interrupts_build"] = True
        after = json.loads(run([*kubectl, "get", "pods", "-o", "json"]).stdout)["items"]
        assert len(after) == 1 and after[0]["metadata"]["uid"] == uid
        results["transport_cut_recovers_same_worker"] = build("ok").returncode == 0
        (context / "Dockerfile").write_text('FROM scratch\nCOPY repro /repro\nRUN ["/repro", "sleep"]\n')
        output = (root / "interrupted-build.log").open("w")
        child = subprocess.Popen(
            [*docker, "buildx", "build", "--builder", "local-kind", "--progress=plain", "--no-cache", str(context)],
            env=env,
            stdout=output,
            stderr=subprocess.STDOUT,
        )
        for _ in range(120):
            if "REPRO_PROGRESS" in (root / "interrupted-build.log").read_text():
                break
            if child.poll() is not None:
                raise RuntimeError("Build ended before disruption")
            time.sleep(0.25)
        else:
            raise RuntimeError("Build did not start within 30 seconds")
        pods = json.loads(run([*kubectl, "get", "pods", "-o", "json"]).stdout)["items"]
        assert len(pods) == 1
        run([*kubectl, "delete", "pod", pods[0]["metadata"]["name"], "--grace-period=0", "--force", "--wait=false"])
        code = child.wait(timeout=60)
        output.close()
        assert code != 0
        results["pod_shutdown_interrupts_build"] = True
        run([*kubectl, "rollout", "status", "deployment", "--timeout=90s"])
        results["replacement_recovers"] = build("ok").returncode == 0
        run([*kubectl, "scale", "deployment", "--all", "--replicas=0"])
        run([*kubectl, "wait", "--for=delete", "pod", "--all", "--timeout=60s"])
        run([*kubectl, "scale", "deployment", "--all", "--replicas=1"])
        run([*kubectl, "rollout", "status", "deployment", "--timeout=90s"])
        results["scale_down_up_recovers"] = build("ok").returncode == 0
        results["buildx_sha256"] = hashlib.sha256(binary.read_bytes()).hexdigest()
        results["buildx_version"] = run([*docker, "buildx", "version"]).stdout.strip()
        results["buildkit_image_id"] = image_id
    finally:
        if child and child.poll() is None:
            child.terminate()
            try:
                child.wait(timeout=10)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait()
        if proxy:
            proxy.close()
        (root / "results.json").write_text(json.dumps(results, indent=2) + "\n")
        if created:
            try:
                for argv in [
                    [*kubectl, "get", "pods", "-o", "yaml"],
                    [*kubectl, "get", "events", "-o", "yaml"],
                    [
                        *kubectl,
                        "logs",
                        "--all-pods=true",
                        "deployment/" + (deployment or "unknown"),
                        "--all-containers=true",
                    ],
                ]:
                    run(argv, check=False, timeout=15)
            finally:
                run([args.kind, "delete", "cluster", "--name", name, "--kubeconfig", str(kubeconfig)], timeout=120)
        print("Evidence retained: " + str(root), flush=True)  # noqa: T201 - CLI result


if __name__ == "__main__":
    main()
