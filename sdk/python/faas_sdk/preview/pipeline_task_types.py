"""Generate a typed client module from a saved scoped manifest."""

from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any, Literal, cast

from ..job_runtime import job_value_hash
from ..job_types import _render_job_types
from .pipeline_start_preview import _preview_contract
from .pipeline_task_manifest import PipelineTaskManifestError, _json, _reply_contract, parse_pipeline_task_manifest

Language = Literal["typescript", "python"]


def generate_pipeline_task_types(value: Any, tool: str, language: Language) -> str:
    if language not in ("typescript", "python"):
        raise ValueError("Unsupported language")
    pipelines = parse_pipeline_task_manifest(value)["pipelines"]
    if tool.startswith("family:"):
        family = tool.removeprefix("family:")
        entry = next(
            (
                item for item in pipelines if "revision" in item
                and item["revision"] == {"family": family, "starts": "default"}
            ),
            None,
        )
    else:
        entry = next((item for item in pipelines if item["tool"] == tool), None)
    if entry is None:
        raise PipelineTaskManifestError("unknown_pipeline")
    ts = language == "typescript"
    header, body = _render_job_types(
        json.loads(_json(entry["contract"], sort_keys=True)), language, "Input", "Output", "contract"
    )
    reply_name = "replyContract" if ts else "reply_contract"
    form = entry["clarification"]
    reply = ""
    if form:
        spec = json.loads(_json(_reply_contract(form["schema"]), sort_keys=True))
        reply = "\n" + _render_job_types(spec, language, "ReplyInput", "Reply", reply_name)[1]
    preview = ""
    if "preview" in entry:
        spec = json.loads(_json(_preview_contract(entry["preview"]), sort_keys=True))
        preview = "\n" + _render_job_types(spec, language, "PreviewInput", "Preview", "previewContract" if ts else "preview_contract")[1]
    fingerprint = job_value_hash(["gregale.pipeline-task-binding.v1", entry])
    names = [item["name"] for item in entry["artifacts"]]
    if ts:
        return (
            header
            + 'import {createPipelineTaskClient, type PipelineTaskTransport} from "@gregale/sdk-node/preview";\n'
            + f"\n// Pipeline Task binding SHA-256 {fingerprint}\n"
            + body
            + reply
            + preview
            + f"\nexport const artifactNames = {_json(names)} as const;\n"
            + "export type ArtifactName = (typeof artifactNames)[number];\n"
            + "export function createClient(transport: PipelineTaskTransport) {\n"
            + f"  return createPipelineTaskClient<Input, Output, {'Reply' if form else 'never'}>"
            + f"({{ tool: {_json(entry['tool'])}, contract, {'replyContract, ' if form else ''}"
            + ("checkContractOnStart: true, " if "revision" in entry else "")
            + "transport });\n"
            + "}\n"
        )
    py_names = ", ".join(_json(name) for name in names)
    annotation = f"Literal[{py_names}]" if py_names else "Never"
    tuple_names = py_names + ("," if py_names else "")
    return (
        header
        + "from typing import Never\nfrom faas_sdk.preview import PipelineTaskClient, PipelineTaskTransport\n"
        + f"\n# Pipeline Task binding SHA-256 {fingerprint}\n"
        + body
        + reply
        + preview
        + f"\nArtifactName: TypeAlias = {annotation}\nartifact_names: tuple[ArtifactName, ...] = ({tuple_names})\n\n"
        + f"def create_client(transport: PipelineTaskTransport) -> PipelineTaskClient[Input, Output, {'Reply' if form else 'Never'}]:\n"
        + f"    return PipelineTaskClient(contract, tool={_json(entry['tool'])}, {'reply_contract=reply_contract, ' if form else ''}"
        + ("check_contract_on_start=True, " if "revision" in entry else "")
        + "transport=transport)\n"
    )


def _main() -> None:
    args = sys.argv[1:]
    try:
        source, tool, language, output, *flags = args
        if language not in ("typescript", "python") or flags not in ([], ["--check"]):
            raise ValueError
        generated = generate_pipeline_task_types(json.loads(Path(source).read_text(encoding="utf-8")), tool, cast(Language, language))
        destination = Path(output)
        if flags:
            if not destination.is_file() or destination.read_bytes() != generated.encode():
                raise PipelineTaskManifestError("drift")
        else:
            destination.write_bytes(generated.encode())
    except Exception as error:
        detail = " " + str(error) if isinstance(error, PipelineTaskManifestError) else ""
        raise SystemExit(
            "Use python -m faas_sdk.preview.pipeline_task_types MANIFEST TOOL|family:NAME typescript|python OUTPUT [--check]."
            + detail
        ) from None


if __name__ == "__main__":
    _main()
