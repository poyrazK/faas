"""Vendor canonical runtime import closures into self-contained CLI starters."""

import argparse
import ast
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SOURCE = ROOT / "sdk/python/faas_sdk"
TARGETS = {
    "rest-api-postgres-python": ["async_postgres_pool_runtime", "async_secret_reload_runtime"],
    "object-storage-python": ["async_object_storage_runtime", "async_secret_reload_runtime"],
}
HEADER = "# Generated from sdk/python; run python sdk/python/scripts/sync_binding_starters.py.\n"
EXTERNAL = {"httpx"}


def runtime_closure(entries):
    modules = {"__init__.py": HEADER}
    pending = list(entries)
    while pending:
        name = pending.pop()
        if name + ".py" in modules:
            continue
        if not name.isidentifier():
            raise ValueError("Unsupported starter runtime module")
        source = (SOURCE / (name + ".py")).read_text()
        modules[name + ".py"] = HEADER + source
        # Include imports inside TYPE_CHECKING, functions and methods too. A
        # regenerated helper must never depend on the repository's SDK barrel.
        for node in ast.walk(ast.parse(source)):
            if isinstance(node, ast.ImportFrom):
                if node.level:
                    if node.level != 1 or not node.module or "." in node.module:
                        raise ValueError("Unsupported relative starter dependency")
                    pending.append(node.module)
                elif node.module.split(".")[0] not in sys.stdlib_module_names | EXTERNAL:
                    raise ValueError("Unsupported external starter dependency")
            elif isinstance(node, ast.Import):
                if any(alias.name.split(".")[0] not in sys.stdlib_module_names | EXTERNAL for alias in node.names):
                    raise ValueError("Unsupported external starter dependency")
            elif isinstance(node, ast.Call) and isinstance(node.func, ast.Name) and node.func.id == "__import__":
                raise ValueError("Dynamic starter runtime import requires review")
    return modules


def sync(check=False):
    for template, entries in TARGETS.items():
        sync_runtime(ROOT / "cmd/gregale/templates" / template / "runtime", entries, check)


def sync_runtime(target, entries, check):
    modules = runtime_closure(entries)
    if not check:
        target.mkdir(parents=True, exist_ok=True)
    for name, source in modules.items():
        file = target / name
        if check:
            if not file.exists() or file.read_text() != source:
                raise ValueError("Python binding starter drift; run sdk/python/scripts/sync_binding_starters.py")
        else:
            file.write_text(source)
    for file in target.iterdir():
        if file.name == "__pycache__" and file.is_dir():
            continue
        if file.name in modules:
            continue
        if not file.is_file() or not file.read_text().startswith(HEADER):
            raise ValueError("Unrecognized file in generated Python runtime")
        if check:
            raise ValueError("Obsolete generated Python runtime")
        file.unlink()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    sync(parser.parse_args().check)
