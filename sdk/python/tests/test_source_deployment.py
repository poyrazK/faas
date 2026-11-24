"""Source workflow interruptions and exact acceptance recovery shared with Node."""

import asyncio
import hashlib
import json
import shutil
import subprocess
import sys
from dataclasses import replace
from pathlib import Path

import httpx
import pytest

from faas_sdk import (
    FaaSClient,
    SourceDeploymentError,
    SourceDeploymentOptions,
    SourceDeploymentPreparationOptions,
    SourceDeploymentWaitOptions,
    load_source_deployment_state,
)
from faas_sdk.models.upload_deploy_options import UploadDeployOptions
from faas_sdk.preview import FaaSClient as PreviewClient

SDK = Path(__file__).resolve().parents[1]
CONTRACT = SDK / "request-contracts/source-deployments.json"
if not CONTRACT.exists():
    CONTRACT = SDK.parent / "request-contracts/source-deployments.json"
FIXTURE = json.loads(CONTRACT.read_text())


@pytest.mark.parametrize("factory", [FaaSClient, PreviewClient], ids=["public", "preview"])
@pytest.mark.parametrize("case", FIXTURE["cases"], ids=lambda c: c["name"])
async def test_shared_workflow_contract(tmp_path, factory, case):
    directory, state_path = tmp_path / "project", tmp_path / "deploy.json"
    directory.mkdir()
    (directory / "index.js").write_text("export default 1;\n")
    scenario = case["scenario"]
    if scenario == "workspace":
        (directory / "apps/api").mkdir(parents=True)
        (directory / "apps/api/main.js").write_text("workspace source")
    metadata = UploadDeployOptions.from_dict(case["deploy_options"]) if "deploy_options" in case else None
    total = commits = creates = deployments = 0
    received, receipt, stopped, recovered = b"", None, False, False
    calls, events = [], []
    stop_scenarios = {
        "preparing",
        "prepared",
        "new_upload",
        "uploading",
        "ready",
        "before_commit",
        "committing",
        "accepted",
        "observing",
        "complete",
        "changed_archive",
        "removed_source",
        "removed_archive_accepted",
        "wrong_base",
        "wrong_app",
        "wrong_metadata",
        "wrong_preparation",
    }
    stop_stage = (
        "prepared"
        if scenario
        in {"changed_archive", "removed_source", "wrong_base", "wrong_app", "wrong_metadata", "wrong_preparation"}
        else "accepted"
        if scenario == "removed_archive_accepted"
        else scenario
    )

    async def handler(request):
        nonlocal total, commits, creates, deployments, received, receipt, recovered
        assert request.headers["Authorization"] == "Bearer before" and request.url.host == "workflow.test"
        method, route = request.method, request.url.path.removeprefix("/prefix")
        calls.append((method, route))
        saved = load_source_deployment_state(state_path)
        assert saved.base_url == "https://workflow.test/prefix"
        assert saved.preparation_sha256 == case.get("preparation_sha256", FIXTURE["preparation_sha256"])
        assert saved.metadata_sha256 == case.get("metadata_sha256", FIXTURE["metadata_sha256"])
        if method == "POST" and route == "/v1/uploads":
            creates += 1
            body = json.loads(request.content)
            total = body["total_size"]
            assert saved.upload.stage == "new" and body["sha256_hex"] == saved.archive.sha256
            return httpx.Response(
                201,
                json={
                    "upload_id": "upload",
                    "chunk_size": 37,
                    "total_size": total,
                    "expires_at": "2026-11-20T00:00:00Z",
                },
            )
        if method == "GET" and route == "/v1/uploads/upload":
            return httpx.Response(
                200,
                json={
                    "upload_id": "upload",
                    "app_slug": "api",
                    "chunk_size": 37,
                    "total_size": total,
                    "received_bytes": len(received),
                    "status": "committed" if receipt else "open",
                    "deployment_id": receipt["deployment_id"] if receipt else None,
                    "expires_at": "2026-11-20T00:00:00Z",
                },
            )
        if method == "PATCH":
            assert int(request.headers["Upload-Offset"]) == len(received)
            if scenario == "lost_chunk" and not recovered:
                recovered = True
                received += request.content[:7]
                raise OSError("partial reply lost")
            received += request.content
            return httpx.Response(200, headers={"Upload-Offset": str(len(received))})
        if method == "GET" and "/deployment-submissions/" in route:
            return httpx.Response(200, json=receipt) if receipt else httpx.Response(404, json={"code": "not_found"})
        if method == "POST" and route.endswith("/commit"):
            commits += 1
            assert (
                saved.upload.stage == "committing"
                and request.headers["Deployment-Attempt-ID"] == saved.upload.attempt_id
            )
            if scenario == "unknown_commit":
                raise OSError("acceptance unknown")
            deployments += 1
            receipt = {
                "attempt_id": saved.upload.attempt_id,
                "app_id": FIXTURE["app_id"],
                "deployment_id": FIXTURE["deployment_id"],
                "accepted_at": "2026-11-14T17:00:00Z",
            }
            if scenario == "commit_timeout":
                await asyncio.Event().wait()
            if scenario == "lost_commit":
                raise OSError("commit reply lost")
            return httpx.Response(201, json={"id": FIXTURE["deployment_id"]})
        if method == "GET" and route.endswith("/timeline"):
            phase = {"blocked": "held", "failed": "failed", "superseded": "superseded", "poll_limit": "building"}.get(
                scenario, "released"
            )
            return httpx.Response(
                200,
                json={
                    "deployment_id": FIXTURE["deployment_id"],
                    "app": "api",
                    "status": "live",
                    "phase": phase,
                    "summary": "test",
                    "traffic_percent": 100,
                    "events": [],
                    "created_at": "2026-11-14T17:00:00Z",
                },
            )
        raise AssertionError(f"Unexpected {method} {route}")

    client = factory(
        base_url="https://workflow.test/prefix",
        token="before",
        timeout=0.001,
        httpx_args={"transport": httpx.MockTransport(handler)},
    )

    def progress(event):
        nonlocal stopped
        events.append(event)
        if scenario == "client_snapshot":
            client.async_httpx_client.headers["Authorization"] = "Bearer after"
            client.async_httpx_client.base_url = "https://changed.test"
        if not stopped and scenario in stop_scenarios:
            match = (
                event.phase == "preparing" and event.source is None
                if stop_stage == "preparing"
                else event.phase == "uploading" and event.upload is None
                if stop_stage == "prepared"
                else event.upload is not None and event.upload.stage == "new"
                if stop_stage == "new_upload"
                else event.phase == "uploading"
                and event.upload is not None
                and event.upload.stage == "uploading"
                and event.upload.received_bytes > 0
                if stop_stage == "uploading"
                else event.upload is not None and event.upload.stage == "ready"
                if stop_stage == "ready"
                else event.phase == "committing" and event.upload is None
                if stop_stage == "before_commit"
                else event.upload is not None and event.upload.stage == "committing"
                if stop_stage == "committing"
                else event.upload is not None and event.upload.stage == "accepted"
                if stop_stage == "accepted"
                else event.phase == stop_stage
            )
            if match:
                stopped = True
                raise RuntimeError("process interrupted after saved boundary")

    options = SourceDeploymentOptions(
        state_path=state_path,
        timeout=1 if scenario == "commit_timeout" else 6,
        wait=SourceDeploymentWaitOptions(min_delay=0.001, max_delay=0.001, max_polls=1),
        on_progress=progress,
    )
    result = None
    try:
        if scenario in stop_scenarios or scenario == "commit_timeout":
            with pytest.raises(SourceDeploymentError):
                await client.deploy_source(directory, "api", options=options)
            assert stopped or scenario == "commit_timeout"
            saved = load_source_deployment_state(state_path)
            archive = tmp_path / f".gregale-deploy-{saved.workflow_id}.tar.gz"
            assert state_path.stat().st_mode & 0o777 == 0o600
            assert "Bearer" not in json.dumps(saved.to_dict())
            if scenario == "changed_archive":
                archive.write_text("changed")
            if scenario in ("removed_source", "removed_archive_accepted"):
                shutil.rmtree(directory)
            if scenario == "removed_archive_accepted":
                archive.unlink()
            if scenario == "wrong_base":
                client.async_httpx_client.base_url = "https://wrong.test"
            before = len(calls)
            resume_options = replace(
                options,
                timeout=6,
                **(
                    {"preparation": SourceDeploymentPreparationOptions(exclude=("index.js",))}
                    if scenario == "wrong_preparation"
                    else {}
                ),
            )
            resume = client.resume_source_deployment(
                "other" if scenario == "wrong_app" else "api",
                options=resume_options,
                deploy_options=UploadDeployOptions(runtime="node") if scenario == "wrong_metadata" else None,
            )
            if case["outcome"] in ("acceptance_unknown", "upload_failed", "state_mismatch"):
                with pytest.raises(SourceDeploymentError) as error:
                    await resume
                assert error.value.code == case["outcome"]
                if case["outcome"] == "state_mismatch" or scenario == "changed_archive":
                    assert len(calls) == before
            else:
                result = await resume
        elif case["outcome"] in ("acceptance_unknown", "wait_failed"):
            with pytest.raises(SourceDeploymentError) as error:
                await client.deploy_source(directory, "api", options=options)
            assert error.value.code == case["outcome"]
            if case["outcome"] == "acceptance_unknown":
                with pytest.raises(SourceDeploymentError) as repeated:
                    await client.resume_source_deployment("api", options=options)
                assert repeated.value.code == "acceptance_unknown"
        else:
            result = await client.deploy_source(directory, "api", options=options, deploy_options=metadata)
        if result is not None:
            assert result.outcome == case["outcome"] and result.deployment_id == FIXTURE["deployment_id"]
        assert commits <= 1 and deployments <= 1 and creates <= 1
        if deployments:
            assert hashlib.sha256(received).hexdigest() == load_source_deployment_state(state_path).archive.sha256
        for left, right in zip(events, events[1:]):
            if right.elapsed_ms < left.elapsed_ms:
                assert stopped or scenario in ("commit_timeout", "unknown_commit")
    finally:
        await client.aclose()
        client.close()


async def test_cancellation_budget_and_same_journal_concurrency(tmp_path):
    directory = tmp_path / "project"
    directory.mkdir()
    (directory / "index.js").write_text("source")
    state_path = tmp_path / "deploy.json"
    entered, release = asyncio.Event(), asyncio.Event()

    async def progress(event):
        entered.set()
        try:
            await release.wait()
        except asyncio.CancelledError:
            await release.wait()

    client = FaaSClient(
        base_url="https://workflow.test",
        httpx_args={
            "transport": httpx.MockTransport(lambda _: (_ for _ in ()).throw(AssertionError("No request expected")))
        },
    )
    options = SourceDeploymentOptions(state_path=state_path, timeout=6, on_progress=progress)
    try:
        task = asyncio.create_task(client.deploy_source(directory, "api", options=options))
        await entered.wait()
        with pytest.raises(SourceDeploymentError) as error:
            await client.deploy_source(directory, "api", options=options)
        assert error.value.code == "busy"
        task.cancel("caller stopped")
        with pytest.raises(asyncio.CancelledError, match="caller stopped") as error:
            await task
        assert error.value.code == "cancelled" and error.value.state.upload is None
        release.set()
        for _ in range(10):
            await asyncio.sleep(0)

        async def never(event):
            await asyncio.Event().wait()

        with pytest.raises(SourceDeploymentError) as error:
            await client.resume_source_deployment("api", options=replace(options, timeout=0.03, on_progress=never))
        assert error.value.code == "timeout"
        with pytest.raises(SourceDeploymentError) as error:
            await client.resume_source_deployment("api", options=replace(options, state_path=tmp_path / "missing.json"))
        assert error.value.code == "invalid_state"

        async def slow_phase(event):
            if event.source is None and event.upload is None and event.release is None:
                await asyncio.sleep(0.08)

        with pytest.raises(SourceDeploymentError) as error:
            await client.deploy_source(
                directory,
                "api",
                options=replace(options, state_path=tmp_path / "budget.json", timeout=0.12, on_progress=slow_phase),
            )
        assert error.value.code == "timeout"
    finally:
        await client.aclose()
        client.close()


async def test_concurrent_accounts_keep_captured_configuration(tmp_path):
    async def run(token):
        directory, state_path = tmp_path / token, tmp_path / f"{token}.json"
        directory.mkdir()
        (directory / "main.js").write_text(token)
        id = FIXTURE["deployment_id"][:-1] + ("1" if token == "a" else "2")
        total = received = commits = 0
        receipt = None

        async def handler(request):
            nonlocal total, received, commits, receipt
            assert request.headers["Authorization"] == f"Bearer {token}" and request.url.host == f"{token}.test"
            path = request.url.path
            if request.method == "POST" and path == "/v1/uploads":
                total = json.loads(request.content)["total_size"]
                return httpx.Response(
                    201,
                    json={
                        "upload_id": "u",
                        "chunk_size": 4096,
                        "total_size": total,
                        "expires_at": "2026-11-20T00:00:00Z",
                    },
                )
            if request.method == "GET" and path == "/v1/uploads/u":
                return httpx.Response(
                    200,
                    json={
                        "upload_id": "u",
                        "app_slug": "api",
                        "chunk_size": 4096,
                        "total_size": total,
                        "received_bytes": received,
                        "status": "open",
                        "expires_at": "2026-11-20T00:00:00Z",
                    },
                )
            if request.method == "PATCH":
                received += len(request.content)
                return httpx.Response(200, headers={"Upload-Offset": str(received)})
            if request.method == "POST":
                commits += 1
                receipt = {
                    "attempt_id": request.headers["Deployment-Attempt-ID"],
                    "app_id": FIXTURE["app_id"],
                    "deployment_id": id,
                    "accepted_at": "2026-11-14T17:00:00Z",
                }
                return httpx.Response(201, json={"id": id})
            if path.endswith("/timeline"):
                return httpx.Response(
                    200,
                    json={
                        "deployment_id": id,
                        "app": "api",
                        "status": "live",
                        "phase": "released",
                        "summary": "done",
                        "traffic_percent": 100,
                        "events": [],
                        "created_at": "2026-11-14T17:00:00Z",
                    },
                )
            return httpx.Response(200, json=receipt) if receipt else httpx.Response(404, json={"code": "not_found"})

        client = FaaSClient(
            base_url=f"https://{token}.test", token=token, httpx_args={"transport": httpx.MockTransport(handler)}
        )

        def progress(event):
            client.async_httpx_client.headers["Authorization"] = "Bearer changed"
            client.async_httpx_client.base_url = "https://changed.test"

        try:
            result = await client.deploy_source(
                directory,
                "api",
                options=SourceDeploymentOptions(state_path=state_path, timeout=6, on_progress=progress),
            )
            assert result.outcome == "succeeded" and commits == 1
            return result
        finally:
            await client.aclose()
            client.close()

    a, b = await asyncio.gather(run("a"), run("b"))
    assert a.workflow_id != b.workflow_id and a.deployment_id != b.deployment_id


async def test_hard_process_stop_at_committing_uses_receipt_reads_only(tmp_path):
    directory, state_path = tmp_path / "project", tmp_path / "state.json"
    directory.mkdir()
    (directory / "main.js").write_text("source")
    program = """
import asyncio,json,os,sys
sys.path.insert(0,sys.argv[1])
import httpx
from faas_sdk import FaaSClient,SourceDeploymentOptions
total=received=0
def handler(request):
    global total,received
    path=request.url.path
    if request.method=="POST" and path=="/v1/uploads":
        total=json.loads(request.content)["total_size"]
        return httpx.Response(201,json={"upload_id":"u","chunk_size":4096,"total_size":total,"expires_at":"2026-11-20T00:00:00Z"})
    if request.method=="GET" and path=="/v1/uploads/u":
        return httpx.Response(200,json={"upload_id":"u","app_slug":"api","chunk_size":4096,"total_size":total,"received_bytes":received,"status":"open","expires_at":"2026-11-20T00:00:00Z"})
    if request.method=="PATCH":
        received+=len(request.content)
        return httpx.Response(200,headers={"Upload-Offset":str(received)})
    return httpx.Response(404,json={"code":"not_found"})
def progress(event):
    if event.upload and event.upload.stage=="committing":os._exit(0)
async def main():
    client=FaaSClient(base_url="https://workflow.test",httpx_args={"transport":httpx.MockTransport(handler)})
    await client.deploy_source(sys.argv[2],"api",options=SourceDeploymentOptions(state_path=sys.argv[3],timeout=6,on_progress=progress))
    raise RuntimeError("did not stop at committing")
asyncio.run(main())
"""
    child = await asyncio.to_thread(
        subprocess.run,
        [sys.executable, "-c", program, str(SDK), str(directory), str(state_path)],
        capture_output=True,
        text=True,
        timeout=12,
    )
    assert child.returncode == 0, child.stdout + child.stderr
    assert load_source_deployment_state(state_path).upload.stage == "committing"
    calls = []

    def handler(request):
        assert request.method == "GET" and "/deployment-submissions/" in request.url.path
        calls.append(request.url.path)
        return httpx.Response(404, json={"code": "not_found"})

    client = FaaSClient(base_url="https://workflow.test", httpx_args={"transport": httpx.MockTransport(handler)})
    try:
        with pytest.raises(SourceDeploymentError) as error:
            await client.resume_source_deployment(
                "api", options=SourceDeploymentOptions(state_path=state_path, timeout=3)
            )
        assert error.value.code == "acceptance_unknown" and len(calls) == 1
    finally:
        await client.aclose()
        client.close()


async def test_corrupt_journals_and_invalid_policies_make_no_requests(tmp_path):
    directory, state_path = tmp_path / "project", tmp_path / "state.json"
    directory.mkdir()
    (directory / "main.js").write_text("source")
    with pytest.raises(ValueError):
        SourceDeploymentWaitOptions(max_polls=0)
    client = FaaSClient(
        base_url="https://workflow.test",
        httpx_args={
            "transport": httpx.MockTransport(lambda _: (_ for _ in ()).throw(AssertionError("unexpected HTTP")))
        },
    )
    try:
        with pytest.raises(SourceDeploymentError) as error:
            await client.deploy_source(
                directory, "api", options=SourceDeploymentOptions(state_path=directory / "state.json", timeout=1)
            )
        assert error.value.code == "state_mismatch"
        state_path.write_text('{"version":99}')
        with pytest.raises(SourceDeploymentError) as error:
            await client.resume_source_deployment(
                "api", options=SourceDeploymentOptions(state_path=state_path, timeout=1)
            )
        assert error.value.code == "invalid_state"
    finally:
        await client.aclose()
        client.close()
