import copy
import json
from pathlib import Path

import pytest
from faas_sdk.preview import (
    PIPELINE_TASK_MANIFEST_URI,
    JobContract,
    PipelineTaskManifestError,
    parse_pipeline_task_manifest,
    read_pipeline_task_manifest,
)
from faas_sdk.preview.pipeline_task_manifest import _build_manifest
from faas_sdk.preview.pipeline_task_types import generate_pipeline_task_types

ROOT = Path(__file__).parents[1] / "request-contracts"
if not ROOT.exists():
    ROOT = Path(__file__).parents[2] / "request-contracts"
F = json.loads((ROOT / "pipeline-task-manifest.json").read_text())


@pytest.mark.asyncio
async def test_published_manifest_validates_both_binding_and_contract_digests():
    class Transport:
        async def request(self, method, params, *, timeout):
            assert method == "resources/read"
            assert params == {"uri": PIPELINE_TASK_MANIFEST_URI}
            return {
                "contents": [{"uri": PIPELINE_TASK_MANIFEST_URI, "mimeType": "application/json", "text": json.dumps(F)}]
            }

    assert await read_pipeline_task_manifest(Transport(), timeout=1) == F
    assert _build_manifest(F["pipelines"]) == F
    with pytest.raises(PipelineTaskManifestError, match="invalid_manifest"):
        parse_pipeline_task_manifest(F | {"sha256": "0" * 64})
    changed = copy.deepcopy(F)
    changed["pipelines"][0]["contract"]["output"]["properties"]["rows"]["type"] = "string"
    with pytest.raises(PipelineTaskManifestError, match="invalid_manifest"):
        _build_manifest(changed["pipelines"])


def test_generation_is_deterministic_and_changes_with_selected_contract():
    ts = generate_pipeline_task_types(F, "csv_import", "typescript")
    assert 'readonly "rows": number' in ts
    assert 'artifactNames = ["report"] as const' in ts
    py = generate_pipeline_task_types(F, "csv_import", "python")
    assert '"email_column": Literal["contact", "alternate_contact"]' in py
    compile(py, "pipeline.py", "exec")
    reordered = json.loads(json.dumps(F, sort_keys=True), object_pairs_hook=lambda pairs: dict(reversed(pairs)))
    assert generate_pipeline_task_types(reordered, "csv_import", "typescript") == ts
    assert generate_pipeline_task_types(reordered, "csv_import", "python") == py
    changed = copy.deepcopy(F["pipelines"])
    changed[0]["contract"]["output"]["properties"]["rows"]["type"] = "string"
    changed[0]["contract_sha256"] = JobContract(changed[0]["contract"]).sha256
    current = _build_manifest(changed)
    assert 'readonly "rows": string' in generate_pipeline_task_types(current, "csv_import", "typescript")
    with pytest.raises(PipelineTaskManifestError, match="unknown_pipeline"):
        generate_pipeline_task_types(F, "hidden_pipeline", "typescript")


def test_bindings_reflect_a_caller_without_clarification_or_artifact_scopes():
    restricted = _build_manifest([entry | {"clarification": None, "artifacts": []} for entry in F["pipelines"]])
    py = generate_pipeline_task_types(restricted, "csv_import", "python")
    assert "PipelineTaskClient[Input, Output, Never]" in py
    assert "ArtifactName: TypeAlias = Never" in py
    assert restricted["sha256"] != F["sha256"]
    compile(py, "pipeline.py", "exec")


def test_versioned_bindings_use_the_visible_default_or_explicit_revision():
    manifest = parse_pipeline_task_manifest(json.loads((ROOT / "pipeline-task-revisions.json").read_text()))
    retained, latest = manifest["pipelines"]
    selector = "family:csv_imports"
    assert latest["revision"]["starts"] == "default"
    for language, option in (("python", "check_contract_on_start=True"), ("typescript", "checkContractOnStart: true")):
        generated = generate_pipeline_task_types(manifest, selector, language)
        assert generated == generate_pipeline_task_types(manifest, latest["tool"], language)
        assert "csv_import_v2" in generated
        assert option in generated
        assert generate_pipeline_task_types(manifest, retained["tool"], language) != generated
    scoped = _build_manifest([retained])
    with pytest.raises(PipelineTaskManifestError, match="unknown_pipeline"):
        generate_pipeline_task_types(scoped, selector, "python")
    with pytest.raises(PipelineTaskManifestError, match="invalid_manifest"):
        _build_manifest([retained | {"revision": {"family": "csv_imports", "starts": "default"}}, latest])
