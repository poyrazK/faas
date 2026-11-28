"""Portable, scope-filtered pipeline Task metadata."""

from __future__ import annotations

import json
import re
from typing import Any, Literal, NotRequired, TypedDict, cast

from ..job_runtime import JobContract, job_value_hash
from .pipeline_start_preview import _preview_contract

PIPELINE_TASK_MANIFEST_URI = "gregale-pipeline://manifest/v1"
PIPELINE_TASK_MANIFEST_MAX_BYTES = 128 * 1024
_DOMAIN = "gregale.pipeline-task-manifest.v1"
_NAME = r"[a-z][a-z0-9_]{0,47}"
_MIME = r"[a-z0-9!#$&^_.+-]+/[a-z0-9!#$&^_.+-]+"


class PipelineTaskManifestError(ValueError):
    def __init__(self, code: Literal["invalid_manifest", "unknown_pipeline", "drift"]):
        super().__init__("Pipeline Task manifest: " + code)
        self.code = code


class PipelineTaskArtifactDescriptor(TypedDict):
    name: str
    mimeType: str
    description: str


class _Clarification(TypedDict):
    message: str
    schema: dict[str, Any]


class PipelineTaskRevision(TypedDict):
    family: str
    starts: Literal["default", "retained"]


class _PipelineContractEntry(TypedDict):
    revision: NotRequired[PipelineTaskRevision]
    preview: NotRequired[dict[str, Any]]
    tool: str
    contract: dict[str, Any]
    contract_sha256: str
    artifacts: list[PipelineTaskArtifactDescriptor]


class PipelineTaskManifestEntry(_PipelineContractEntry):
    clarification: _Clarification | None


class PipelineTaskManifest(TypedDict):
    version: Literal[1]
    sha256: str
    pipelines: list[PipelineTaskManifestEntry]


class _PipelineContractMetadata(TypedDict):
    tool: str
    spec: dict[str, Any]
    revision: NotRequired[PipelineTaskRevision]
    preview: NotRequired[dict[str, Any]]


def _json(value: Any, *, sort_keys: bool = False) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False, sort_keys=sort_keys)


def _object(value: Any, keys: set[str]) -> dict[str, Any]:
    if not isinstance(value, dict) or set(value) != keys:
        raise PipelineTaskManifestError("invalid_manifest")
    return value


def _revision(value: Any) -> PipelineTaskRevision:
    raw = _object(value, {"family", "starts"})
    family, starts = raw["family"], raw["starts"]
    if (
        not isinstance(family, str)
        or not re.fullmatch(_NAME, family)
        or not isinstance(starts, str)
        or starts not in ("default", "retained")
    ):
        raise PipelineTaskManifestError("invalid_manifest")
    return PipelineTaskRevision(family=family, starts=cast(Literal["default", "retained"], starts))


def _reply_contract(schema: Any) -> dict[str, Any]:
    return {"version": 1, "name": "pipeline-task-form", "input": {"type": "null"}, "output": schema}


def _entries(value: Any) -> list[PipelineTaskManifestEntry]:
    if not isinstance(value, list) or len(value) > 32:
        raise PipelineTaskManifestError("invalid_manifest")
    entries: list[PipelineTaskManifestEntry] = []
    tools: set[str] = set()
    defaults: set[str] = set()
    for candidate in value:
        keys = {"tool", "contract", "contract_sha256", "clarification", "artifacts"}
        if isinstance(candidate, dict):
            keys.update(candidate.keys() & {"revision", "preview"})
        raw = _object(candidate, keys)
        revision = _revision(raw["revision"]) if "revision" in raw else None
        if revision and revision["starts"] == "default":
            family = revision["family"]
            if family in defaults:
                raise PipelineTaskManifestError("invalid_manifest")
            defaults.add(family)
        tool, artifacts = raw["tool"], raw["artifacts"]
        if (
            not isinstance(tool, str)
            or not re.fullmatch(_NAME, tool)
            or tool in tools
            or not isinstance(artifacts, list)
            or len(artifacts) > 32
        ):
            raise PipelineTaskManifestError("invalid_manifest")
        tools.add(tool)
        contract = JobContract[Any, Any](raw["contract"])
        if raw["contract_sha256"] != contract.sha256:
            raise PipelineTaskManifestError("invalid_manifest")
        form = None
        if raw["clarification"] is not None:
            item = _object(raw["clarification"], {"message", "schema"})
            message = item["message"]
            reply = JobContract[Any, Any](_reply_contract(item["schema"])).spec["output"]
            if (
                not isinstance(message, str)
                or not message.strip()
                or len(message.encode("utf-16-le")) > 2048
                or reply["type"] != "object"
                or not reply["properties"]
                or any(
                    field["type"] not in ("string", "number", "integer", "boolean")
                    for field in reply["properties"].values()
                )
                or len(_json(reply).encode()) > 16384
            ):
                raise PipelineTaskManifestError("invalid_manifest")
            form = _Clarification(message=message, schema=reply)
        published: list[PipelineTaskArtifactDescriptor] = []
        names: set[str] = set()
        for candidate in artifacts:
            item = _object(candidate, {"name", "mimeType", "description"})
            name, mime, description = item["name"], item["mimeType"], item["description"]
            if (
                not isinstance(name, str)
                or not re.fullmatch(_NAME, name)
                or name in names
                or not isinstance(mime, str)
                or len(mime) > 128
                or not re.fullmatch(_MIME, mime)
                or not isinstance(description, str)
                or not description.strip()
                or len(description.encode("utf-16-le")) > 1024
            ):
                raise PipelineTaskManifestError("invalid_manifest")
            names.add(name)
            published.append(PipelineTaskArtifactDescriptor(name=name, mimeType=mime, description=description))
        entry = PipelineTaskManifestEntry(
            tool=tool,
            contract=contract.spec,
            contract_sha256=contract.sha256,
            clarification=form,
            artifacts=sorted(published, key=lambda item: item["name"]),
        )
        if revision:
            entry["revision"] = revision
        if "preview" in raw:
            entry["preview"] = JobContract[Any, Any](_preview_contract(raw["preview"])).spec["output"]
        entries.append(entry)
    return sorted(entries, key=lambda item: item["tool"])


def _build_manifest(entries: Any) -> PipelineTaskManifest:
    try:
        pipelines = _entries(entries)
        result = PipelineTaskManifest(version=1, sha256=job_value_hash([_DOMAIN, 1, pipelines]), pipelines=pipelines)
        if len(_json(result).encode()) > PIPELINE_TASK_MANIFEST_MAX_BYTES:
            raise PipelineTaskManifestError("invalid_manifest")
        return result
    except Exception:
        raise PipelineTaskManifestError("invalid_manifest") from None


def parse_pipeline_task_manifest(value: Any) -> PipelineTaskManifest:
    try:
        if len(_json(value).encode()) > PIPELINE_TASK_MANIFEST_MAX_BYTES:
            raise PipelineTaskManifestError("invalid_manifest")
        raw = _object(value, {"version", "sha256", "pipelines"})
        if type(raw["version"]) is not int or raw["version"] != 1:
            raise PipelineTaskManifestError("invalid_manifest")
        manifest = _build_manifest(raw["pipelines"])
        if raw["sha256"] != manifest["sha256"]:
            raise PipelineTaskManifestError("invalid_manifest")
        return manifest
    except Exception:
        raise PipelineTaskManifestError("invalid_manifest") from None
