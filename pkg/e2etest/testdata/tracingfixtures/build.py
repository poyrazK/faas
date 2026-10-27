#!/usr/bin/env python3
"""Assemble the managed-runtime tracing fixtures for cmd/e2e's metal tests.

Each fixture is the runner base's pinned runtime image (images/runner-*.Dockerfile)
plus one layer holding the platform tracing bundle at /opt/gregale/tracing,
installed the way the runner image installs it, and a plain app with no
tracing code. The output directory holds image.json, the config and the gzip
layers that e2etest.PrebuiltImage serves (ADR-958, ADR-213 runtime adapter).

    python3 build.py node   OUT  # FAAS_E2E_NODE_TRACING_IMAGE_DIR
    python3 build.py python OUT  # FAAS_E2E_PYTHON_TRACING_IMAGE_DIR

Run from any checkout with network access, npm and pip.
"""
import gzip
import hashlib
import io
import json
import os
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, "..", "..", "..", ".."))
INDEX_TYPES = (
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
)
MANIFEST_ACCEPT = ",".join(INDEX_TYPES + (
    "application/vnd.oci.image.manifest.v1+json",
    "application/vnd.docker.distribution.manifest.v2+json",
))
PIP_PLATFORM = ["--platform", "manylinux_2_28_x86_64", "--platform", "manylinux_2_17_x86_64",
                "--platform", "manylinux2014_x86_64", "--implementation", "cp",
                "--python-version", "3.12", "--only-binary=:all:"]


def base_ref(dockerfile):
    with open(os.path.join(REPO, "images", dockerfile)) as f:
        match = re.search(r"^FROM ([^/\s]+)/([^:@\s]+)(?::\S+)?@(sha256:[0-9a-f]{64})", f.read(), re.M)
    if not match:
        sys.exit("no pinned FROM in " + dockerfile)
    return match.groups()


class Registry:
    def __init__(self, host, repo):
        self.host, self.repo, self.token = host, repo, None

    def get(self, kind, ref, accept=None):
        url = f"https://{self.host}/v2/{self.repo}/{kind}/{ref}"
        for _ in range(2):
            req = urllib.request.Request(url, headers={"Accept": accept} if accept else {})
            if self.token:
                req.add_header("Authorization", "Bearer " + self.token)
            try:
                with urllib.request.urlopen(req) as resp:
                    return resp.read()
            except urllib.error.HTTPError as err:
                if err.code != 401 or self.token:
                    raise
                self.token = self._token(err.headers.get("WWW-Authenticate", ""))
        raise RuntimeError("unauthorized: " + url)

    def _token(self, challenge):
        params = dict(re.findall(r'(\w+)="([^"]*)"', challenge))
        query = f"?service={params.get('service', '')}&scope=repository:{self.repo}:pull"
        with urllib.request.urlopen(params["realm"] + query) as resp:
            body = json.load(resp)
        return body.get("token") or body["access_token"]


def pull(out, dockerfile):
    host, repo, digest = base_ref(dockerfile)
    registry = Registry(host, repo)
    manifest = json.loads(registry.get("manifests", digest, MANIFEST_ACCEPT))
    if manifest.get("mediaType") in INDEX_TYPES or "manifests" in manifest:
        amd64 = next(m for m in manifest["manifests"]
                     if m.get("platform", {}).get("architecture") == "amd64"
                     and m.get("platform", {}).get("os") == "linux")
        manifest = json.loads(registry.get("manifests", amd64["digest"], MANIFEST_ACCEPT))
    config = json.loads(registry.get("blobs", manifest["config"]["digest"]))
    layers = []
    for i, layer in enumerate(manifest["layers"]):
        blob = registry.get("blobs", layer["digest"])
        if "sha256:" + hashlib.sha256(blob).hexdigest() != layer["digest"]:
            sys.exit("layer digest mismatch: " + layer["digest"])
        name = f"base-{i}.tar.gz"
        with open(os.path.join(out, name), "wb") as f:
            f.write(blob)
        layers.append({"file": name, "digest": layer["digest"], "size": len(blob)})
    return config, layers


def run(*args, cwd=None):
    subprocess.run(args, cwd=cwd, check=True)


def stage_node(root):
    tracing = os.path.join(root, "opt", "gregale", "tracing")
    os.makedirs(tracing)
    for name in ("package.json", "package-lock.json"):
        shutil.copy(os.path.join(REPO, "guest", "tracing", "node", name), tracing)
    shutil.copy(os.path.join(REPO, "guest", "tracing", "node.cjs"), tracing)
    run("npm", "ci", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund", cwd=tracing)
    app = os.path.join(root, "app")
    os.makedirs(app)
    for name in ("server.js", "package.json"):
        shutil.copy(os.path.join(HERE, "node", name), app)
    run("npm", "install", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund", cwd=app)
    return ["node", "/app/server.js"], []


def stage_python(root):
    tracing = os.path.join(root, "opt", "gregale", "tracing", "python")
    lib, deps = os.path.join(tracing, "lib"), os.path.join(tracing, "deps")
    os.makedirs(deps)
    run(sys.executable, "-m", "pip", "install", "--no-cache-dir", "--require-hashes", *PIP_PLATFORM,
        "--target", lib, "-r", os.path.join(REPO, "guest", "tracing", "python", "requirements.txt"))
    # Same split as images/runner-python312.Dockerfile: OpenTelemetry in lib,
    # its third-party dependencies in deps.
    for name in os.listdir(lib):
        if not name.startswith("opentelemetry"):
            shutil.move(os.path.join(lib, name), deps)
    shutil.copy(os.path.join(REPO, "guest", "tracing", "python", "sitecustomize.py"), tracing)
    app = os.path.join(root, "app")
    run(sys.executable, "-m", "pip", "install", "--no-cache-dir", *PIP_PLATFORM,
        "--target", os.path.join(app, "pkgs"), "-r", os.path.join(HERE, "python", "requirements.txt"))
    shutil.copy(os.path.join(HERE, "python", "app.py"), app)
    return ["python", "/app/app.py"], ["PYTHONPATH=/app/pkgs"]


def owned_by_root(info):
    info.uid = info.gid = 0
    info.uname = info.gname = "root"
    info.mtime = 0
    info.mode = 0o755 if info.isdir() or info.mode & 0o111 else 0o644
    return info


def app_layer(out, root):
    raw = io.BytesIO()
    with tarfile.open(fileobj=raw, mode="w", format=tarfile.PAX_FORMAT) as tw:
        for top in sorted(os.listdir(root)):
            tw.add(os.path.join(root, top), arcname=top, filter=owned_by_root)
    tar_bytes = raw.getvalue()
    blob = gzip.compress(tar_bytes, mtime=0)
    with open(os.path.join(out, "app.tar.gz"), "wb") as f:
        f.write(blob)
    layer = {"file": "app.tar.gz", "digest": "sha256:" + hashlib.sha256(blob).hexdigest(), "size": len(blob)}
    return layer, "sha256:" + hashlib.sha256(tar_bytes).hexdigest()


def main():
    if len(sys.argv) != 3 or sys.argv[1] not in ("node", "python"):
        sys.exit(__doc__)
    runtime, out = sys.argv[1], os.path.abspath(sys.argv[2])
    os.makedirs(out, exist_ok=True)
    dockerfile = {"node": "runner-node22.Dockerfile", "python": "runner-python312.Dockerfile"}[runtime]
    config, layers = pull(out, dockerfile)
    with tempfile.TemporaryDirectory() as root:
        cmd, env = (stage_node if runtime == "node" else stage_python)(root)
        layer, diff_id = app_layer(out, root)
    layers.append(layer)
    image_config = config.setdefault("config", {})
    image_config.update({"Entrypoint": None, "Cmd": cmd, "WorkingDir": "/app",
                         "ExposedPorts": {"8080/tcp": {}}})
    image_config["Env"] = (image_config.get("Env") or []) + env
    config["rootfs"]["diff_ids"].append(diff_id)
    config.setdefault("history", []).append({"created_by": "gregale e2e tracing fixture"})
    with open(os.path.join(out, "config.json"), "w") as f:
        json.dump(config, f)
    with open(os.path.join(out, "image.json"), "w") as f:
        json.dump({"config": "config.json", "layers": layers}, f, indent=2)
    print(out)


if __name__ == "__main__":
    main()
