"""Project discovery and workflow contracts shared with Node and CLI helpers."""

import asyncio
import io
import json
import shutil
import tarfile
from pathlib import Path

import httpx
import pytest

from faas_sdk import (
    FaaSClient,
    ProjectInspectionError,
    ProjectInspectionOptions,
    ProjectPlan,
    SourceDeploymentError,
    SourceDeploymentOptions,
    SourceDeploymentPreparationOptions,
    inspect_project,
    load_source_deployment_state,
    validate_project_plan,
)
from faas_sdk.preview import inspect_project as inspect_preview_project

CONTRACT = Path(__file__).resolve().parents[1] / "request-contracts/project-inspection.json"
if not CONTRACT.exists():
    CONTRACT = Path(__file__).resolve().parents[2] / "request-contracts/project-inspection.json"
FIXTURE = json.loads(CONTRACT.read_text())


def options(values):
    return ProjectInspectionOptions(
        **{
            {
                "contextRoot": "context_root",
                "sourceRoot": "source_root",
                "functionName": "function_name",
                "appSlug": "app_slug",
            }.get(k, k): v
            for k, v in values.items()
        }
    )


@pytest.mark.parametrize("inspect", [inspect_project, inspect_preview_project], ids=["public", "preview"])
@pytest.mark.parametrize("case", FIXTURE["cases"], ids=lambda c: c["name"])
async def test_shared_project_inspection(tmp_path, inspect, case):
    for name, body in case["files"].items():
        path = tmp_path / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(body)
    plan = await inspect(tmp_path / case["directory"], options=options(case["options"]))
    wire = plan.to_dict()
    for key, value in case["expected"].items():
        actual = (
            [i.code for i in plan.issues if i.severity == "error"]
            if key == "errors"
            else [i.code for i in plan.issues if i.severity == "warning"]
            if key == "warnings"
            else Path(plan.context_root).relative_to(tmp_path).as_posix()
            if key == "context"
            else [i.root for i in plan.candidates if i.kind == "workspace"]
            if key == "workspaceRoots"
            else [i.purpose for i in plan.generated_files]
            if key == "generatedPurposes"
            else wire[key]
        )
        assert actual == value, key
    assert ProjectPlan.from_dict(json.loads(json.dumps(wire))) == plan
    assert "private-value" not in json.dumps(wire) and "private-reference" not in json.dumps(wire)
    if "fingerprint" in case:
        assert plan.fingerprint == case["fingerprint"]
    if not any(i.severity == "error" for i in plan.issues):
        await validate_project_plan(plan)


async def test_inspection_limits_stale_inputs_and_cancellation(tmp_path):
    root = tmp_path / "project"
    root.mkdir()
    (root / "package.json").write_text("{}")
    plan = await inspect_project(root)
    (root / "package.json").write_text('{"name":"changed"}')
    with pytest.raises(ProjectInspectionError, match="changed"):
        await validate_project_plan(plan)
    with pytest.raises(ProjectInspectionError):
        ProjectPlan.from_dict({**plan.to_dict(), "runtime": "changed"})
    (root / "README.md").write_text("source")
    for opts in [
        ProjectInspectionOptions(max_entries=0),
        ProjectInspectionOptions(max_entries=1),
        ProjectInspectionOptions(timeout=0.000001),
    ]:
        with pytest.raises(ProjectInspectionError):
            await inspect_project(root, options=opts)
    task = asyncio.create_task(inspect_project(root))
    await asyncio.sleep(0)
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task
    (root / "gregale.yaml").write_text("x" * (1024 * 1024 + 1))
    with pytest.raises(ProjectInspectionError):
        await inspect_project(root)
    (root / "gregale.yaml").unlink()
    (tmp_path / "outside").write_text("private-value")
    (root / "gregale.yaml").symlink_to(tmp_path / "outside")
    escaped = await inspect_project(root)
    assert any(i.code == "unsafe_path" for i in escaped.issues)
    (root / "gregale.yaml").unlink()
    (tmp_path / "outside-dir").mkdir()
    (tmp_path / "outside-dir/handler.js").write_text("private-value")
    (root / "fn").symlink_to(tmp_path / "outside-dir", target_is_directory=True)
    (root / "gregale.yaml").write_text("functions:\n  - {name: one, root: fn}\n")
    escaped_function = await inspect_project(root)
    assert any(i.code == "unsafe_path" for i in escaped_function.issues)
    ProjectPlan.from_dict(escaped_function.to_dict())


@pytest.mark.parametrize("language", ["js", "go"])
async def test_project_workflow_virtual_files_binding_and_resume(tmp_path, language):
    root = tmp_path / "project"
    source = root / "functions/one"
    source.mkdir(parents=True)
    (root / "gregale.yaml").write_text("functions:\n  - {name: one, root: functions/one}\n")
    (source / ("handler." + language)).write_text("// source\n")
    project = await inspect_project(root, options=ProjectInspectionOptions(app_slug="api"))
    assert not project.issues
    state_path = tmp_path / "state.json"
    calls, received, paused, receipt = [], bytearray(), False, None
    deployment_id = "11111111-1111-4111-8111-111111111111"

    async def handler(request):
        nonlocal receipt
        calls.append(request)
        if request.method == "POST" and request.url.path == "/v1/uploads":
            return httpx.Response(
                201,
                json={
                    "upload_id": "upload",
                    "chunk_size": 65536,
                    "total_size": json.loads(request.content)["total_size"],
                    "expires_at": "2026-11-20T00:00:00Z",
                },
            )
        if request.method == "GET" and request.url.path == "/v1/uploads/upload":
            return httpx.Response(
                200,
                json={
                    "upload_id": "upload",
                    "app_slug": "api",
                    "chunk_size": 65536,
                    "total_size": load_source_deployment_state(state_path).archive.size,
                    "received_bytes": len(received),
                    "status": "open",
                    "expires_at": "2026-11-20T00:00:00Z",
                },
            )
        if request.method == "PATCH":
            received.extend(request.content)
            return httpx.Response(200, headers={"Upload-Offset": str(len(received))})
        if "/deployment-submissions/" in request.url.path:
            return httpx.Response(200, json=receipt) if receipt else httpx.Response(404, json={"code": "not_found"})
        if request.method == "POST" and request.url.path.endswith("/commit"):
            receipt = {
                "attempt_id": load_source_deployment_state(state_path).upload.attempt_id,
                "app_id": deployment_id,
                "deployment_id": deployment_id,
                "accepted_at": "2026-11-14T17:00:00Z",
            }
            return httpx.Response(201, json={"id": deployment_id})
        if request.url.path.endswith("/timeline"):
            return httpx.Response(
                200,
                json={
                    "deployment_id": deployment_id,
                    "app": "api",
                    "status": "live",
                    "phase": "released",
                    "summary": "test",
                    "traffic_percent": 100,
                    "events": [],
                    "created_at": "2026-11-14T17:00:00Z",
                },
            )
        raise AssertionError(str(request.url))

    def progress(event):
        nonlocal paused
        if not paused and event.phase == "uploading" and event.upload is None:
            paused = True
            raise RuntimeError("pause after preparation")

    client = FaaSClient(base_url="https://project.test", httpx_args={"transport": httpx.MockTransport(handler)})
    try:
        with pytest.raises(SourceDeploymentError):
            await client.deploy_project(
                project, options=SourceDeploymentOptions(state_path=state_path, timeout=5, on_progress=progress)
            )
        assert paused and not calls
        saved = load_source_deployment_state(state_path)
        archive = (tmp_path / f".gregale-deploy-{saved.workflow_id}.tar.gz").read_bytes()
        with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as tar:
            manifest = json.load(tar.extractfile("gregale.yaml"))
            assert manifest == {
                "function": {"runtime": "node22" if language == "js" else "go124", "handler": "handler.handler"}
            }
            if language == "go":
                assert tar.extractfile("go.mod").read() == b"module gregale-function\n\ngo 1.26.11\n"
        assert not (source / "gregale.yaml").exists() and not (source / "go.mod").exists()
        changed = await inspect_project(
            root,
            options=ProjectInspectionOptions(app_slug="api", runtime="node24" if language == "js" else "go124-alpine"),
        )
        with pytest.raises(SourceDeploymentError):
            await client.resume_project_deployment(
                changed, options=SourceDeploymentOptions(state_path=state_path, timeout=5)
            )
        assert not calls
        shutil.rmtree(root)
        result = await client.resume_project_deployment(
            ProjectPlan.from_dict(project.to_dict()), options=SourceDeploymentOptions(state_path=state_path, timeout=5)
        )
        assert result.outcome == "succeeded" and bytes(received) == archive
    finally:
        await client.aclose()


async def test_project_workflow_stale_or_excluded_inputs_prevent_http(tmp_path):
    root = tmp_path / "project"
    root.mkdir()
    (root / "package.json").write_text("{}")
    project = await inspect_project(root, options=ProjectInspectionOptions(app_slug="api"))

    async def unexpected(request):
        raise AssertionError("HTTP before validation")

    client = FaaSClient(base_url="https://project.test", httpx_args={"transport": httpx.MockTransport(unexpected)})
    try:
        (root / "package.json").write_text('{"name":"changed"}')
        with pytest.raises(SourceDeploymentError):
            await client.deploy_project(
                project, options=SourceDeploymentOptions(state_path=tmp_path / "stale.json", timeout=5)
            )
        fresh = await inspect_project(root, options=ProjectInspectionOptions(app_slug="api"))
        from faas_sdk.models.upload_deploy_options import UploadDeployOptions

        with pytest.raises(SourceDeploymentError):
            await client.deploy_source(
                fresh.context_root,
                "api",
                deploy_options=UploadDeployOptions(source_root=".", runtime="node22"),
                options=SourceDeploymentOptions(state_path=tmp_path / "metadata.json", timeout=5, project=fresh),
            )
        with pytest.raises(SourceDeploymentError):
            await client.deploy_project(
                fresh,
                options=SourceDeploymentOptions(
                    state_path=tmp_path / "excluded.json",
                    timeout=5,
                    preparation=SourceDeploymentPreparationOptions(exclude=("package.json",)),
                ),
            )
    finally:
        await client.aclose()


async def test_generated_project_files_honor_ignores_and_limits(tmp_path):
    root = tmp_path / "project"
    root.mkdir()
    (root / "handler.go").write_text("// source\n")

    async def unexpected(request):
        raise AssertionError("HTTP before validation")

    client = FaaSClient(base_url="https://project.test", httpx_args={"transport": httpx.MockTransport(unexpected)})
    try:
        for name, preparation in [
            ("exclude", SourceDeploymentPreparationOptions(exclude=("go.mod",))),
            ("entries", SourceDeploymentPreparationOptions(max_entries=1)),
            ("bytes", SourceDeploymentPreparationOptions(max_bytes=10)),
        ]:
            project = await inspect_project(root, options=ProjectInspectionOptions(app_slug="api"))
            with pytest.raises(SourceDeploymentError):
                await client.deploy_project(
                    project,
                    options=SourceDeploymentOptions(
                        state_path=tmp_path / (name + ".json"), timeout=5, preparation=preparation
                    ),
                )
        (root / ".gregaleignore").write_text("go.mod\n")
        project = await inspect_project(root, options=ProjectInspectionOptions(app_slug="api"))
        with pytest.raises(SourceDeploymentError):
            await client.deploy_project(
                project, options=SourceDeploymentOptions(state_path=tmp_path / "ignored.json", timeout=5)
            )
    finally:
        await client.aclose()
