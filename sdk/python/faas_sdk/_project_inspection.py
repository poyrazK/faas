"""Read-only project discovery. Never evaluates project code or invokes a shell."""

from __future__ import annotations

import asyncio
import hashlib
import json
import os
import posixpath
import re
import stat
import time
import tomllib
from dataclasses import asdict, dataclass, fields
from pathlib import Path
from typing import Any, Literal

from ruamel.yaml import YAML

from .models.upload_deploy_options import UploadDeployOptions

ProjectShape = Literal["app", "function", "unknown"]
ProjectFramework = Literal["docker", "node", "python", "go", "unknown"]
ProjectIssueCode = Literal[
    "invalid_manifest",
    "unsafe_path",
    "ambiguous_selection",
    "missing_input",
    "unsupported_settings",
    "unknown_project",
    "validation_deferred",
]


@dataclass(frozen=True, slots=True)
class ProjectIssue:
    code: ProjectIssueCode
    severity: Literal["error", "warning"]
    path: str
    message: str


@dataclass(frozen=True, slots=True)
class ProjectInput:
    path: str
    kind: Literal["file", "directory"]
    sha256: str | None


@dataclass(frozen=True, slots=True)
class ProjectSetting:
    field: str
    value: str | bool | None
    origin: Literal["override", "manifest", "detection", "default"]
    file: str | None = None
    key: str | None = None


@dataclass(frozen=True, slots=True)
class ProjectCandidate:
    name: str
    root: str
    kind: Literal["workspace", "function"]


@dataclass(frozen=True, slots=True)
class ProjectGeneratedFile:
    path: str
    size: int
    sha256: str
    purpose: Literal["function_manifest", "go_module"]


@dataclass(frozen=True, slots=True)
class ProjectBuild:
    dockerfile: str
    target: str | None
    argument_names: tuple[str, ...]
    secret_ids: tuple[str, ...]


@dataclass(frozen=True, slots=True)
class ProjectPlan:
    version: int
    project_root: str
    context_root: str
    source_root: str
    app_slug: str | None
    shape: ProjectShape
    framework: ProjectFramework
    runtime: str | None
    handler: str | None
    dockerfile: bool
    function_name: str | None
    build: ProjectBuild | None
    candidates: tuple[ProjectCandidate, ...]
    settings: tuple[ProjectSetting, ...]
    issues: tuple[ProjectIssue, ...]
    inputs: tuple[ProjectInput, ...]
    generated_files: tuple[ProjectGeneratedFile, ...]
    fingerprint: str

    @property
    def deploy_options(self) -> UploadDeployOptions:
        values: dict[str, Any] = {"source_root": self.source_root}
        if self.shape == "function":
            values.update(runtime=self.runtime, handler=self.handler)
        if self.dockerfile:
            values["dockerfile"] = True
        return UploadDeployOptions.from_dict(values)

    def to_dict(self) -> dict[str, Any]:
        result = {_camel(f.name): getattr(self, f.name) for f in fields(self)}
        for name in ("candidates", "settings", "issues", "inputs"):
            result[name] = [asdict(item) for item in result[name]]
        result["generatedFiles"] = [asdict(item) for item in self.generated_files]
        result["build"] = (
            {
                "dockerfile": self.build.dockerfile,
                "target": self.build.target,
                "argumentNames": list(self.build.argument_names),
                "secretIds": list(self.build.secret_ids),
            }
            if self.build
            else None
        )
        return result

    @classmethod
    def from_dict(cls, value: dict[str, Any]) -> ProjectPlan:
        try:
            if set(value) != {_camel(f.name) for f in fields(cls)}:
                raise ValueError()
            data = {f.name: value[_camel(f.name)] for f in fields(cls)}
            for name, kind in (
                ("candidates", ProjectCandidate),
                ("settings", ProjectSetting),
                ("issues", ProjectIssue),
                ("inputs", ProjectInput),
                ("generated_files", ProjectGeneratedFile),
            ):
                data[name] = tuple(kind(**item) for item in data[name])
            b = data["build"]
            data["build"] = (
                ProjectBuild(b["dockerfile"], b["target"], tuple(b["argumentNames"]), tuple(b["secretIds"]))
                if b
                else None
            )
            plan = cls(**data)
            if (
                plan.version != 1
                or not Path(plan.project_root).is_absolute()
                or not Path(plan.context_root).is_absolute()
                or not Path(plan.context_root).is_relative_to(plan.project_root)
                or _source_root(plan.source_root) != plan.source_root
                or len(plan.inputs) > 40000
                or len(plan.generated_files) > 2
                or _fingerprint(plan) != plan.fingerprint
            ):
                raise ValueError()
            for item in plan.inputs:
                if (
                    (item.path != "." and _source_root(item.path) != item.path)
                    or item.kind not in ("file", "directory")
                    or (item.sha256 is not None and not re.fullmatch(r"[a-f0-9]{64}", item.sha256))
                    or (item.kind == "directory" and item.sha256 is None)
                ):
                    raise ValueError()
            if (
                plan.shape not in ("app", "function", "unknown")
                or plan.framework not in ("docker", "node", "python", "go", "unknown")
                or type(plan.version) is not int
                or type(plan.dockerfile) is not bool
                or not all(
                    v is None or isinstance(v, str)
                    for v in (plan.app_slug, plan.runtime, plan.handler, plan.function_name)
                )
                or len({i.path for i in plan.inputs}) != len(plan.inputs)
                or len({i.path for i in plan.generated_files}) != len(plan.generated_files)
            ):
                raise ValueError()
            for setting in plan.settings:
                if (
                    not isinstance(setting.field, str)
                    or setting.origin not in ("override", "manifest", "detection", "default")
                    or not (setting.value is None or type(setting.value) in (str, bool))
                    or not all(v is None or isinstance(v, str) for v in (setting.file, setting.key))
                ):
                    raise ValueError()
            for issue in plan.issues:
                if (
                    issue.severity not in ("error", "warning")
                    or issue.code
                    not in (
                        "invalid_manifest",
                        "unsafe_path",
                        "ambiguous_selection",
                        "missing_input",
                        "unsupported_settings",
                        "unknown_project",
                        "validation_deferred",
                    )
                    or not isinstance(issue.path, str)
                    or not isinstance(issue.message, str)
                ):
                    raise ValueError()
            for candidate in plan.candidates:
                if (
                    candidate.kind not in ("workspace", "function")
                    or not isinstance(candidate.name, str)
                    or _source_root(candidate.root) != candidate.root
                ):
                    raise ValueError()
            for generated in plan.generated_files:
                if (
                    generated.purpose not in ("function_manifest", "go_module")
                    or (posixpath.dirname(generated.path) or ".") != plan.source_root
                    or posixpath.basename(generated.path)
                    != ("go.mod" if generated.purpose == "go_module" else "gregale.yaml")
                ):
                    raise ValueError()
            if plan.build and (
                _local_path(plan.build.dockerfile) != plan.build.dockerfile
                or plan.build.target is not None
                and not isinstance(plan.build.target, str)
                or not all(isinstance(v, str) for v in (*plan.build.argument_names, *plan.build.secret_ids))
            ):
                raise ValueError()
            for path, body in project_build_files(plan).items():
                if _local_path(path) != path or posixpath.basename(path) not in ("go.mod", "gregale.yaml"):
                    raise ValueError()
                generated_item = next(i for i in plan.generated_files if i.path == path)
                if generated_item.size != len(body.encode()) or generated_item.sha256 != _sum(body):
                    raise ValueError()
            return plan
        except (ValueError, TypeError, KeyError, AttributeError, StopIteration):
            raise ProjectInspectionError("invalid_plan", "Invalid project plan") from None


@dataclass(frozen=True, kw_only=True)
class ProjectInspectionOptions:
    context_root: str | os.PathLike[str] | None = None
    source_root: str | None = None
    function_name: str | None = None
    app_slug: str | None = None
    runtime: str | None = None
    handler: str | None = None
    dockerfile: bool | None = None
    timeout: float = 120
    max_entries: int = 40000


class ProjectInspectionError(Exception):
    def __init__(
        self,
        code: Literal["invalid_input", "limit_exceeded", "deadline_exceeded", "project_changed", "invalid_plan"],
        message: str,
    ):
        super().__init__(message)
        self.code = code


_MARKERS = (
    ("Dockerfile", "docker"),
    ("package.json", "node"),
    ("requirements.txt", "python"),
    ("pyproject.toml", "python"),
    ("Pipfile", "python"),
    ("setup.py", "python"),
    ("go.mod", "go"),
)
_HANDLERS = {"handler.js": "node22", "handler.ts": "node22", "handler.py": "python312", "handler.go": "go124"}
_SKIP = {".git", "node_modules", "vendor", "__pycache__", "dist", ".next", "coverage", "target", ".venv", ".cache"}
_KEYS = set(
    "schema_version profiling tracing hosting build dev function functions release lifecycle scaling retry_policy queue_bindings triggers event_triggers work_policies exclusive_operations operations operation_workflows async_routes routes companions main_depends_on extensions workflows databases buckets worker realtime labels project".split()
)


def _camel(name: str) -> str:
    first, *rest = name.split("_")
    return first + "".join(s.title() for s in rest)


def _stable(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))


def _sum(value: str | bytes) -> str:
    return hashlib.sha256(value.encode() if isinstance(value, str) else value).hexdigest()


def _source_root(value: str) -> str:
    raw = value.strip()
    if raw in ("", "."):
        return "."
    if (
        len(raw.encode()) > 256
        or "\\" in raw
        or ":" in raw
        or re.search(r"[\x00-\x1f\x7f]", raw)
        or raw.startswith("/")
        or any(p in ("", ".", "..") for p in raw.split("/"))
    ):
        raise ValueError("Invalid source root")
    return raw


def _local_path(value: str) -> str:
    p = posixpath.normpath(value.strip().removeprefix("./"))
    if (
        p in ("", ".", "..")
        or p.startswith("../")
        or value.startswith("/")
        or "\\" in value
        or re.search(r"[\x00-\x1f\x7f]", value)
    ):
        raise ValueError("Path must stay inside the source root")
    return p


def _object(value: Any) -> dict[str, Any]:
    if not isinstance(value, dict) or not all(isinstance(k, str) for k in value):
        raise ValueError("Expected mapping")
    return value


def _string(value: Any, fallback: str = "") -> str:
    if value is None:
        return fallback
    if not isinstance(value, str):
        raise ValueError("Expected string")
    return value


def _keys(value: dict[str, Any], allowed: list[str]) -> None:
    if set(value) - set(allowed):
        raise ValueError("Unknown configuration key")


def _bounded(value: Any, active: set[int] | None = None, depth: int = 0, budget: list[int] | None = None) -> None:
    active, budget = set() if active is None else active, [0] if budget is None else budget
    budget[0] += 1
    if budget[0] > 20000 or depth > 64:
        raise ProjectInspectionError("limit_exceeded", "Configuration exceeds inspection limits")
    if isinstance(value, (dict, list)):
        if id(value) in active:
            raise ValueError("Cyclic configuration")
        active.add(id(value))
        for child in value.values() if isinstance(value, dict) else value:
            _bounded(child, active, depth + 1, budget)
        active.remove(id(value))


def _yaml(text: str) -> Any:
    parser = YAML(typ="safe", pure=True)
    parser.version = (1, 2)
    try:
        value = parser.load(text)
    except Exception:
        raise ValueError("Invalid YAML") from None
    _bounded(value)
    return value


def _decode(name: str, text: str) -> Any:
    value = (
        json.loads(text) if name.endswith(".json") else tomllib.loads(text) if name.endswith(".toml") else _yaml(text)
    )
    _bounded(value)
    return value


class _Reader:
    def __init__(self, root: Path, options: ProjectInspectionOptions):
        self.root, self.options = root, options
        if (
            not 0 < options.timeout <= 2147483.647
            or type(options.max_entries) is not int
            or not 1 <= options.max_entries <= 40000
        ):
            raise ProjectInspectionError("invalid_input", "Invalid inspection limits")
        self.deadline = time.monotonic() + options.timeout
        self.inputs: dict[str, ProjectInput] = {}
        self.directories: dict[str, list[str]] = {}
        self.entries = 0

    def check(self) -> None:
        if time.monotonic() >= self.deadline:
            raise ProjectInspectionError("deadline_exceeded", "Project inspection deadline exceeded")

    def _read(self, path: str) -> bytes | None:
        self.check()
        absolute = self.root / path
        try:
            target = absolute.resolve(strict=True)
        except FileNotFoundError:
            return None
        if not target.is_relative_to(self.root):
            raise ValueError("Path leaves project")
        fd = os.open(target, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
        with os.fdopen(fd, "rb") as file:
            before = os.fstat(file.fileno())
            if not stat.S_ISREG(before.st_mode) or before.st_size > 1024 * 1024:
                raise ProjectInspectionError("limit_exceeded", "Inspection input must be a regular file at most 1 MiB")
            data = file.read(1024 * 1024 + 1)
            after = os.fstat(file.fileno())
            if (
                len(data) > 1024 * 1024
                or (before.st_ino, before.st_size, before.st_mtime_ns, before.st_ctime_ns)
                != (after.st_ino, after.st_size, after.st_mtime_ns, after.st_ctime_ns)
                or absolute.resolve(strict=True) != target
            ):
                raise ProjectInspectionError("project_changed", "Inspection input changed")
            return data

    async def file(self, path: str) -> str | None:
        self.check()
        path = posixpath.normpath(path)
        data = await asyncio.to_thread(self._read, path)
        self.inputs[path] = ProjectInput(path, "file", _sum(data) if data is not None else None)
        self.check()
        return data.decode() if data is not None else None

    async def names(self, path: str) -> list[str]:
        path = posixpath.normpath(path)
        self.check()
        if path in self.directories:
            return self.directories[path]
        absolute = (self.root / path).resolve(strict=True)
        if not absolute.is_relative_to(self.root):
            raise ValueError("Directory leaves project")
        names, description = [], []
        with os.scandir(absolute) as entries:
            for entry in entries:
                self.check()
                self.entries += 1
                if self.entries > self.options.max_entries:
                    raise ProjectInspectionError("limit_exceeded", "Project has too many inspection entries")
                names.append(entry.name)
                kind = (
                    "directory" if entry.is_dir(follow_symlinks=False) else "symlink" if entry.is_symlink() else "file"
                )
                description.append(entry.name + ":" + kind)
        result = sorted(names)
        self.directories[path] = result
        self.inputs[path] = ProjectInput(path, "directory", _sum(_stable(sorted(description))))
        await asyncio.sleep(0)
        return result

    async def regular(self, path: str) -> bool:
        try:
            target = (self.root / path).resolve(strict=True)
            if not target.is_relative_to(self.root):
                raise ValueError("Path leaves project")
            return target.is_file()
        except FileNotFoundError:
            return False


def _workspace_pattern(raw: str) -> tuple[bool, re.Pattern] | None:
    p = raw.strip()
    exclude = p.startswith("!")
    if exclude:
        p = p[1:].strip()
    p = p.removeprefix("./").rstrip("/")
    if not p or p.startswith("/") or "\\" in p or re.search(r"[\[\]{}()]", p):
        raise ValueError("Invalid workspace pattern")
    if any(s in ("", ".", "..") for s in p.split("/")):
        return None
    expression, i = "^", 0
    while i < len(p):
        if p[i : i + 2] == "**":
            if p[i + 2 : i + 3] == "/":
                expression += "(?:[^/]+/)*"
                i += 3
            else:
                expression += ".*"
                i += 2
        elif p[i] == "*":
            expression += "[^/]*"
            i += 1
        elif p[i] == "?":
            expression += "[^/]"
            i += 1
        else:
            expression += re.escape(p[i])
            i += 1
    return exclude, re.compile(expression + "$")


async def _workspace_members(r: _Reader) -> list[str]:
    patterns: list[list[str]] = []
    literal: list[str] = []
    nx = False
    for name in ("package.json", "pnpm-workspace.yaml", "turbo.json", "nx.json", "go.work", "Cargo.toml"):
        text = await r.file(name)
        if text is None:
            continue
        if name == "go.work":
            block = False
            for line in text.splitlines():
                line = re.sub(r"//.*$", "", line).strip()
                if re.match(r"use\s*\(", line):
                    block = True
                    line = re.sub(r"^use\s*\(", "", line).strip()
                elif line.startswith("use "):
                    line = line[4:].strip()
                elif not block:
                    continue
                if line == ")":
                    block = False
                    continue
                if line:
                    literal.append((json.loads(line) if line.startswith('"') else line).removeprefix("./"))
            continue
        data = _object(_decode(name, text))
        if name == "package.json" and "workspaces" in data:
            w = data["workspaces"]
            patterns.append(w if isinstance(w, list) else _object(w)["packages"])
        if name == "pnpm-workspace.yaml" and "packages" in data:
            patterns.append(data["packages"])
        if name == "turbo.json":
            for key in ("pipeline", "$pipeline"):
                if data.get(key):
                    literal.extend(_object(data[key]))
        if name == "nx.json":
            nx = True
            for key, value in _object(data.get("projects", {})).items():
                literal.append(value if isinstance(value, str) else _object(value).get("root", key))
        if name == "Cargo.toml" and data.get("workspace"):
            w = _object(data["workspace"])
            patterns.append(list(w.get("members", [])) + ["!" + p for p in w.get("exclude", [])])
    if not patterns and not literal and not nx:
        return []
    directories = []

    async def walk(path: str, depth: int) -> None:
        if depth > 64:
            raise ProjectInspectionError("limit_exceeded", "Project nesting exceeds 64")
        for name in await r.names(path):
            if name in _SKIP or name.startswith("."):
                continue
            p = posixpath.join(path, name).removeprefix("./")
            info = (r.root / p).lstat()
            if stat.S_ISDIR(info.st_mode):
                directories.append(p)
                await walk(p, depth + 1)
            elif nx and name in ("project.json", "package.json"):
                text = await r.file(p)
                if text:
                    d = _object(_decode(p, text))
                    config = d if name == "project.json" else d.get("nx")
                    if isinstance(config, dict):
                        literal.append(config.get("root", posixpath.dirname(p)))

    await walk(".", 0)
    selected: set[str] = set()
    for group in patterns:
        if not isinstance(group, list) or any(not isinstance(p, str) for p in group):
            raise ValueError("Workspace paths must be strings")
        local: set[str] = set()
        for raw in group:
            match = _workspace_pattern(raw)
            if match:
                exclude, pattern = match
                for directory in directories:
                    if pattern.fullmatch(directory):
                        local.discard(directory) if exclude else local.add(directory)
        selected.update(local)
    for raw in literal:
        p = re.sub(r"^(\./)+", "", raw.strip()).rstrip("/")
        if p and not p.startswith("..") and all(v not in ("", ".") for v in p.split("/")) and p in directories:
            selected.add(p)
    result = []
    for member in sorted(selected):
        for marker in ("Dockerfile", "dockerfile", "package.json", "pyproject.toml", "go.mod", "Cargo.toml", "pom.xml"):
            if await r.regular(posixpath.join(member, marker)):
                result.append(member)
                break
    return result


def _fingerprint(plan: ProjectPlan) -> str:
    value = plan.to_dict()
    value["projectRoot"] = "."
    value["contextRoot"] = Path(plan.context_root).relative_to(plan.project_root).as_posix()
    value.pop("fingerprint")
    return _sum(_stable(value))


def project_build_files(plan: ProjectPlan) -> dict[str, str]:
    return {
        item.path: "module gregale-function\n\ngo 1.26.11\n"
        if item.purpose == "go_module"
        else json.dumps(
            {"function": {"runtime": plan.runtime, "handler": plan.handler}}, ensure_ascii=False, separators=(",", ":")
        )
        + "\n"
        for item in plan.generated_files
    }


async def validate_project_plan(plan: ProjectPlan, *, options: ProjectInspectionOptions | None = None) -> None:
    p = ProjectPlan.from_dict(plan.to_dict())
    if any(i.severity == "error" for i in p.issues):
        raise ProjectInspectionError("invalid_plan", "Project plan has blocking issues")
    r = _Reader(Path(p.project_root), options or ProjectInspectionOptions())
    for item in p.inputs:
        if item.kind == "file":
            await r.file(item.path)
        else:
            await r.names(item.path)
        if r.inputs[item.path].sha256 != item.sha256:
            raise ProjectInspectionError("project_changed", "Project configuration changed after inspection")


async def inspect_project(
    directory: str | os.PathLike[str], *, options: ProjectInspectionOptions | None = None
) -> ProjectPlan:
    opts = options or ProjectInspectionOptions()
    try:
        async with asyncio.timeout(opts.timeout):
            return await _inspect(directory, opts)
    except TimeoutError:
        raise ProjectInspectionError("deadline_exceeded", "Project inspection deadline exceeded") from None


async def _inspect(directory: str | os.PathLike[str], opts: ProjectInspectionOptions) -> ProjectPlan:
    selected = Path(directory).resolve(strict=True)
    root = Path(opts.context_root).resolve(strict=True) if opts.context_root is not None else selected
    if not selected.is_relative_to(root):
        raise ProjectInspectionError("invalid_input", "Directory must stay inside the inspection boundary")
    if opts.context_root is None:
        cursor = selected
        for _ in range(64):
            if (cursor / ".git").exists():
                root = cursor
                break
            if cursor.parent == cursor:
                break
            cursor = cursor.parent
    r = _Reader(root, opts)
    issues: list[ProjectIssue] = []
    settings: list[ProjectSetting] = []
    candidates: list[ProjectCandidate] = []

    def issue(code: ProjectIssueCode, path: str, message: str, severity: Literal["error", "warning"] = "error") -> None:
        issues.append(ProjectIssue(code, severity, path, message))

    def setting(
        field: str, value: str | bool | None, origin: Any, file: str | None = None, key: str | None = None
    ) -> None:
        settings.append(ProjectSetting(field, value, origin, file, key))

    context, source = selected, "."
    runtime = handler = function_name = None
    shape: ProjectShape = "unknown"
    framework: ProjectFramework = "unknown"
    docker, app_slug, build = False, opts.app_slug, None
    generated = []
    try:
        members = await _workspace_members(r)
        candidates.extend(ProjectCandidate(posixpath.basename(m), m, "workspace") for m in members)
        rel = selected.relative_to(root).as_posix()
        if opts.source_root is not None:
            source, context = _source_root(opts.source_root), root
        elif rel in members:
            source, context = rel, root
        elif selected == root and members:
            issue("ambiguous_selection", ".", "Choose sourceRoot explicitly for a workspace repository")
            context = root
        app_dir = context / source
        if not app_dir.resolve(strict=True).is_relative_to(root):
            raise ValueError("Source root leaves project")

        async def load(directory: Path) -> tuple[dict[str, Any], str | None]:
            prefix = directory.relative_to(root).as_posix()
            for name in ("gregale.yaml", "gregale.yml"):
                path = posixpath.join(prefix, name).removeprefix("./")
                text = await r.file(path)
                if text is not None:
                    data = _object(_yaml(text))
                    if (
                        set(data) - _KEYS
                        or isinstance(data.get("schema_version"), bool)
                        or data.get("schema_version", 0) not in (0, 1)
                    ):
                        raise ValueError("Unknown manifest key or unsupported schema version")
                    return data, path
            path = posixpath.join(prefix, "gregale.toml").removeprefix("./")
            if await r.file(path) is not None:
                issue("unsupported_settings", path, "TOML deployment declarations require the CLI")
            return {}, None

        manifest, manifest_path = await load(app_dir)
        if manifest.get("project"):
            p = _object(manifest["project"])
            _keys(p, ["name", "app", "service_policy", "services"])
            if app_slug is None and "app" in p:
                app_slug = _string(p["app"])
                setting("appSlug", app_slug, "manifest", manifest_path, "project.app")
        if manifest.get("function") and manifest.get("functions"):
            raise ValueError("function and functions are mutually exclusive")
        fn = None
        if "functions" in manifest:
            declarations = manifest["functions"]
            if not isinstance(declarations, list) or not declarations:
                raise ValueError("functions must be a nonempty list")
            names: set[str] = set()
            for raw in declarations:
                f = _object(raw)
                name, h, root_path = _string(f.get("name")), _string(f.get("handler")), _string(f.get("root")).strip()
                if not re.fullmatch(r"[a-z]([a-z0-9-]{0,28}[a-z0-9])?", name) or name in names:
                    raise ValueError("Invalid or duplicate function name")
                names.add(name)
                if not root_path and not h:
                    raise ValueError("Function root or handler is required")
                cleaned = posixpath.normpath(root_path) if root_path else "."
                if (
                    cleaned == "."
                    and not h
                    or cleaned == ".."
                    or cleaned.startswith("../")
                    or root_path.startswith("/")
                    or "\\" in root_path
                ):
                    raise ValueError("Function root leaves project")
                candidates.append(
                    ProjectCandidate(
                        name,
                        posixpath.normpath(posixpath.join(app_dir.relative_to(root).as_posix(), cleaned)),
                        "function",
                    )
                )
                if opts.function_name == name or len(declarations) == 1 and opts.function_name is None:
                    fn, function_name = f, name
            if fn is None:
                issue("ambiguous_selection", manifest_path or ".", "Select functionName from the declared functions")
            else:
                if (
                    set(fn) - {"name", "root", "runtime", "handler"}
                    or set(manifest) - {"schema_version", "functions", "project"}
                    or (manifest.get("project") and set(_object(manifest["project"])) - {"name", "app"})
                ):
                    issue(
                        "unsupported_settings",
                        manifest_path or ".",
                        "Function resource settings require separate CLI reconciliation",
                    )
                next_dir = (app_dir / (_string(fn.get("root")) or ".")).resolve(strict=True)
                if not next_dir.is_relative_to(root):
                    raise ValueError("Function root leaves project")
                app_dir = next_dir
                relative_app = app_dir.relative_to(root).as_posix()
                if relative_app in members:
                    context, source = root, relative_app
                else:
                    context, source = app_dir, "."
        elif opts.function_name is not None:
            issue("ambiguous_selection", manifest_path or ".", "No matching functions declaration")
        names_list = await r.names(app_dir.relative_to(root).as_posix())
        for marker, kind in _MARKERS:
            match = next((n for n in names_list if n.lower() == marker.lower()), None)
            if match and await r.regular(posixpath.join(app_dir.relative_to(root).as_posix(), match)):
                await r.file(posixpath.join(app_dir.relative_to(root).as_posix(), match))
                if framework == "unknown":
                    framework = kind  # type: ignore[assignment]
        if fn is not None:
            shape, runtime, handler = "function", _string(fn.get("runtime")) or None, _string(fn.get("handler")) or None
            if runtime and runtime not in (
                "node22",
                "node24",
                "python312",
                "python313",
                "go124",
                "go124-alpine",
                "binary",
            ):
                raise ValueError("Unknown function runtime")
            if handler:
                handler = _local_path(handler)
                if runtime and runtime.startswith("go"):
                    raise ValueError("Go functions require their own root")
                if not await r.regular(posixpath.join(app_dir.relative_to(root).as_posix(), handler)):
                    issue("missing_input", handler, "Declared function handler is missing")
                runtime = runtime or (
                    "python312" if handler.endswith(".py") else "node22" if re.search(r"\.(js|mjs)$", handler) else None
                )
            elif runtime:
                handler = "handler.handler"
            else:
                child, child_path = await load(app_dir)
                if child.get("function"):
                    f = _object(child["function"])
                    _keys(f, ["runtime", "handler"])
                    runtime, handler = _string(f.get("runtime")).strip(), _string(f.get("handler")).strip()
                    if not runtime or not handler:
                        raise ValueError("function runtime and handler are required")
                    setting("runtime", runtime, "manifest", child_path, "function.runtime")
                    setting("handler", handler, "manifest", child_path, "function.handler")
            if runtime and not any(s.field == "runtime" for s in settings):
                setting(
                    "runtime",
                    runtime,
                    "manifest" if fn.get("runtime") else "detection",
                    manifest_path,
                    f"functions.{function_name}.runtime",
                )
            if handler and not any(s.field == "handler" for s in settings):
                setting(
                    "handler",
                    handler,
                    "manifest" if fn.get("handler") else "default",
                    manifest_path,
                    f"functions.{function_name}.handler",
                )
        elif manifest.get("function"):
            f = _object(manifest["function"])
            _keys(f, ["runtime", "handler"])
            runtime, handler = _string(f.get("runtime")).strip(), _string(f.get("handler")).strip()
            if not runtime or not handler:
                raise ValueError("function runtime and handler are required")
            shape = "function"
            setting("runtime", runtime, "manifest", manifest_path, "function.runtime")
            setting("handler", handler, "manifest", manifest_path, "function.handler")
        elif manifest.get("build") is not None or framework != "unknown":
            shape = "app"
        conventional = [n for n in names_list if n.lower() in _HANDLERS]
        if shape == "unknown" and len(conventional) == 1:
            shape = "function"
        if shape == "unknown" and len(conventional) > 1:
            issue("ambiguous_selection", ".", "Several conventional handler files are present")
        if shape == "function" and runtime is None and len(conventional) == 1:
            runtime, handler = _HANDLERS[conventional[0].lower()], "handler.handler"
            setting("runtime", runtime, "detection", (app_dir / conventional[0]).relative_to(root).as_posix())
            setting("handler", handler, "default")
        if opts.runtime is not None:
            runtime, shape = opts.runtime, "function"
            setting("runtime", runtime, "override")
        if opts.handler is not None:
            handler, shape = opts.handler, "function"
            setting("handler", handler, "override")
        if shape == "function":
            if not runtime or not handler:
                issue("missing_input", ".", "Set the function runtime and handler")
            framework = (
                "node"
                if runtime and runtime.startswith("node")
                else "python"
                if runtime and runtime.startswith("python")
                else "go"
                if runtime and runtime.startswith("go")
                else "unknown"
            )
            if handler and re.search(r"\.(js|mjs|ts|py|go)$", handler):
                handler_path = _local_path(handler)
                if not await r.regular(posixpath.join(app_dir.relative_to(root).as_posix(), handler_path)):
                    issue("missing_input", handler_path, "Declared handler is missing")
        if manifest.get("build") is not None:
            if shape == "function":
                raise ValueError("build and function settings are incompatible")
            b = _object(manifest["build"])
            _keys(b, ["dockerfile", "target", "args", "secrets"])
            file = _local_path(_string(b.get("dockerfile"), "Dockerfile") or "Dockerfile")
            arguments, secrets = _object(b.get("args", {})), b.get("secrets", [])
            if not isinstance(secrets, list):
                raise ValueError("Invalid build secrets")
            ids = []
            for raw in secrets:
                v = _object(raw)
                _keys(v, ["id", "secret"])
                if not _string(v.get("id")) or not _string(v.get("secret")):
                    raise ValueError("Invalid build secret reference")
                ids.append(v["id"])
            build = ProjectBuild(file, _string(b.get("target")) or None, tuple(sorted(arguments)), tuple(sorted(ids)))
            docker, framework = True, "docker"
            setting("dockerfile", True, "manifest", manifest_path, "build")
            if not await r.regular(posixpath.join(app_dir.relative_to(root).as_posix(), file)):
                issue("missing_input", file, "Selected Dockerfile is missing")
            else:
                await r.file(posixpath.join(app_dir.relative_to(root).as_posix(), file))
        else:
            docker = framework == "docker"
        if opts.dockerfile is not None:
            docker = opts.dockerfile
            setting("dockerfile", docker, "override")
            if docker:
                shape, framework = "app", "docker"
                if not await r.regular(posixpath.join(app_dir.relative_to(root).as_posix(), "Dockerfile")):
                    issue("missing_input", "Dockerfile", "Dockerfile override requires Dockerfile")
        if opts.app_slug is not None:
            setting("appSlug", opts.app_slug, "override")
        if app_slug is not None and not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]*[a-z0-9])?", app_slug):
            raise ValueError("Invalid app slug")
        if shape == "unknown":
            issue("unknown_project", ".", "No supported build marker or function handler was found")
        setting(
            "sourceRoot",
            source,
            "override" if opts.source_root is not None else "detection" if source != "." else "default",
        )
        if not any(s.field == "dockerfile" for s in settings):
            setting("dockerfile", docker, "detection")
        for key in manifest:
            if key not in ("schema_version", "function", "functions", "build", "project"):
                issue(
                    "validation_deferred",
                    manifest_path or ".",
                    "Section " + key + " requires server or CLI validation",
                    "warning",
                )
        generated_data = {}
        if fn is not None and runtime and handler:
            generated_data[posixpath.join(source, "gregale.yaml").removeprefix("./")] = (
                json.dumps(
                    {"function": {"runtime": runtime, "handler": handler}}, ensure_ascii=False, separators=(",", ":")
                )
                + "\n",
                "function_manifest",
            )
        if (
            shape == "function"
            and runtime
            and runtime.startswith("go")
            and not await r.regular(posixpath.join(app_dir.relative_to(root).as_posix(), "go.mod"))
        ):
            generated_data[posixpath.join(source, "go.mod").removeprefix("./")] = (
                "module gregale-function\n\ngo 1.26.11\n",
                "go_module",
            )
        for path in sorted(generated_data):
            body, purpose = generated_data[path]
            generated.append(ProjectGeneratedFile(path, len(body.encode()), _sum(body), purpose))  # type: ignore[arg-type]
    except ProjectInspectionError:
        raise
    except (ValueError, TypeError, KeyError, OSError, RuntimeError) as error:
        issue(
            "unsafe_path" if "leave" in str(error) else "invalid_manifest",
            ".",
            "Project configuration is invalid; check selected paths and manifest fields",
        )
    plan = ProjectPlan(
        1,
        str(root),
        str(context),
        source,
        app_slug,
        shape,
        framework,
        runtime,
        handler,
        docker,
        function_name,
        build,
        tuple(candidates),
        tuple(settings),
        tuple(issues),
        tuple(r.inputs[k] for k in sorted(r.inputs)),
        tuple(generated),
        "",
    )
    return ProjectPlan(**{**{f.name: getattr(plan, f.name) for f in fields(plan)}, "fingerprint": _fingerprint(plan)})
