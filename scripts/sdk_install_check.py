#!/usr/bin/env python3
"""Build a public SDK tree and verify it from an independent consumer.

Run sdk_mirror.py build first, then:
  python3 scripts/sdk_install_check.py --repo gregale-js --tree /tmp/js --out /tmp/js-install

Requires Go for the shared localhost-only fakeapid fixture, plus npm for Node
or Python 3.11+ with pip/venv for Python. No Gregale account is used. Package
dependencies may be downloaded. Output contains the verified npm tarball or
Python wheel and a JSON verification report; nothing is published.
"""
from __future__ import annotations

import argparse
from contextlib import contextmanager
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time
from urllib.request import build_opener, ProxyHandler

ROOT = Path(__file__).resolve().parents[1]
SAMPLES = ROOT / "sdk/install-tests"
REPOS = ("gregale-go", "gregale-js", "gregale-python")


def run(*args: str, cwd: Path, capture: bool = False, env: dict | None = None) -> str:
    print("+ " + " ".join(map(str, args)), flush=True)
    result = subprocess.run(
        list(map(str, args)), cwd=cwd, check=True, text=True,
        stdout=subprocess.PIPE if capture else None, timeout=600,
        env={**os.environ, **(env or {})},
    )
    return result.stdout or ""


def quickstart(package: Path) -> str:
    """Require one runnable example; a removed marker must fail the gate."""
    readme = (package / "README.md").read_text()
    start, end = "<!-- sdk-install-quickstart:start -->", "<!-- sdk-install-quickstart:end -->"
    if readme.count(start) != 1 or readme.count(end) != 1:
        raise RuntimeError(f"{package}/README.md: expected one marked installation quickstart")
    block = readme.split(start, 1)[1].split(end, 1)[0].strip()
    match = re.fullmatch(r"```(?:js|python|go)\n(.*?)\n```", block, re.DOTALL)
    if not match:
        raise RuntimeError(f"{package}/README.md: quickstart must contain one code fence")
    return match.group(1) + "\n"


def example_env(base_url: str) -> dict:
    return {"FAAS_API": base_url, "FAAS_TOKEN": "sdk-install-fixture"}


@contextmanager
def fixture(work: Path):
    binary = work / "fakeapid"
    run("go", "build", "-trimpath", "-o", str(binary), ".", cwd=ROOT / "sdk/fakeapid")
    with (work / "fakeapid.log").open("w+") as log:
        process = subprocess.Popen(
            [str(binary)], cwd=work, env={**os.environ, "PORT": "0"},
            stdout=subprocess.DEVNULL, stderr=log,
        )
        try:
            deadline = time.monotonic() + 15
            opener = build_opener(ProxyHandler({}))
            while time.monotonic() < deadline:
                log.seek(0)
                output = log.read()
                match = re.search(r"listening on (http://127\.0\.0\.1:\d+)", output)
                if process.poll() is not None:
                    raise RuntimeError(f"fakeapid exited: {output}")
                if match:
                    url = match.group(1)
                    with opener.open(url + "/__healthz", timeout=2) as response:
                        if json.load(response) != {"ok": True}:
                            raise RuntimeError("fakeapid health check failed")
                    yield url
                    return
                time.sleep(0.05)
            raise RuntimeError("fakeapid did not start within 15 seconds")
        finally:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)


def check_node(tree: Path, consumer: Path, out: Path, base_url: str) -> dict:
    package = tree / "packages/sdk-node"
    metadata = json.loads((package / "package.json").read_text())
    run("npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund", cwd=package)
    run("npm", "run", "build", cwd=package)
    packed = json.loads(run(
        "npm", "pack", "--ignore-scripts", "--json", "--pack-destination", str(out),
        cwd=package, capture=True,
    ))
    artifact = out / packed[0]["filename"]
    # Install a tarball, never a file: directory link back to the source tree.
    (consumer / "package.json").write_text(json.dumps({"private": True, "type": "module"}))
    run(
        "npm", "install", "--ignore-scripts", "--no-audit", "--no-fund",
        str(artifact), "typescript@5.6.3", "@types/node@22.10.0", cwd=consumer,
    )
    shutil.copyfile(SAMPLES / "node/consumer.ts", consumer / "consumer.ts")
    (consumer / "tsconfig.json").write_text(json.dumps({
        "compilerOptions": {
            "target": "ES2022", "module": "NodeNext", "moduleResolution": "NodeNext",
            "strict": True, "noEmitOnError": True, "outDir": "dist",
        },
        "include": ["consumer.ts"],
    }))
    run("node", "node_modules/typescript/bin/tsc", "-p", ".", cwd=consumer)
    run("node", "dist/consumer.js", base_url, cwd=consumer)
    (consumer / "quickstart.mjs").write_text(quickstart(package))
    run("node", "quickstart.mjs", cwd=consumer, env=example_env(base_url))
    return {"name": metadata["name"], "version": metadata["version"], "artifact": artifact.name}


def check_python(tree: Path, consumer: Path, out: Path, base_url: str) -> dict:
    run(
        sys.executable, "-m", "pip", "wheel", "--disable-pip-version-check", "--no-deps",
        "--wheel-dir", str(out), str(tree), cwd=consumer,
    )
    wheels = list(out.glob("gregale_sdk-*.whl"))
    if len(wheels) != 1:
        raise RuntimeError("expected exactly one gregale-sdk wheel")
    run(sys.executable, "-m", "venv", str(consumer / "venv"), cwd=consumer)
    python = consumer / "venv" / ("Scripts/python.exe" if os.name == "nt" else "bin/python")
    run(
        str(python), "-m", "pip", "install", "--disable-pip-version-check",
        str(wheels[0]), cwd=consumer,
    )
    shutil.copyfile(SAMPLES / "python/consumer.py", consumer / "consumer.py")
    # -I excludes the checkout, PYTHONPATH and user site-packages.
    version = run(
        str(python), "-I", "-c",
        "from importlib.metadata import version; print(version('gregale-sdk'))",
        cwd=consumer, capture=True,
    ).strip()
    run(str(python), "-I", "consumer.py", base_url, cwd=consumer)
    (consumer / "quickstart.py").write_text(quickstart(tree))
    run(str(python), "-I", "quickstart.py", cwd=consumer, env=example_env(base_url))
    return {"name": "gregale-sdk", "version": version, "artifact": wheels[0].name}


def check_go(tree: Path, consumer: Path, out: Path, base_url: str) -> dict:
    run("go", "build", "-trimpath", "./...", cwd=tree)
    run("go", "vet", "./...", cwd=tree)
    shutil.copyfile(SAMPLES / "go/main.go", consumer / "main.go")
    shutil.copyfile(SAMPLES / "go/go.mod", consumer / "go.mod")
    run(
        "go", "mod", "edit", f"-replace=github.com/gregale-dev/gregale-go={tree}",
        cwd=consumer,
    )
    run("go", "run", "-trimpath", "main.go", base_url, cwd=consumer)
    (consumer / "quickstart.go").write_text(quickstart(tree))
    run("go", "run", "-trimpath", "quickstart.go", cwd=consumer, env=example_env(base_url))
    return {"name": "github.com/gregale-dev/gregale-go", "version": None, "artifact": None}


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--repo", required=True, choices=REPOS)
    parser.add_argument("--tree", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    args = parser.parse_args(argv)
    tree, out = args.tree.resolve(), args.out.resolve()
    if not tree.is_dir():
        parser.error("--tree must be an existing public tree from sdk_mirror.py build")
    if out == tree or tree in out.parents:
        parser.error("--out must be outside --tree")
    if out.exists() and (not out.is_dir() or any(out.iterdir())):
        parser.error("--out must be a new or empty directory")
    out.mkdir(parents=True, exist_ok=True)

    checks = {"gregale-js": check_node, "gregale-python": check_python, "gregale-go": check_go}
    try:
        with tempfile.TemporaryDirectory(prefix="gregale-sdk-install-") as tmp:
            work = Path(tmp)
            consumer = work / "consumer"
            consumer.mkdir()
            with fixture(work) as base_url:
                package = checks[args.repo](tree, consumer, out, base_url)
        artifact = package.pop("artifact")
        report = {
            "schema_version": 1, "repository": args.repo, "package": package,
            "checks": ["independent_consumer", "public_api", "typed_not_found", "readme_quickstart"],
            "artifacts": [],
        }
        if artifact:
            report["artifacts"].append({
                "filename": artifact,
                "sha256": hashlib.sha256((out / artifact).read_bytes()).hexdigest(),
            })
        (out / "sdk-install.json").write_text(json.dumps(report, indent=2) + "\n")
        print(f"{args.repo}: installed consumer verified; report: {out / 'sdk-install.json'}")
        return 0
    except (subprocess.SubprocessError, OSError, ValueError, RuntimeError) as error:
        print(f"sdk_install_check: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
