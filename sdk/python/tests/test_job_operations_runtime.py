import asyncio
import json
from datetime import datetime, timedelta, timezone

import httpx
import pytest

from faas_sdk.job_operations_runtime import GregaleJobOperations
from faas_sdk.models.operation_artifact_request import OperationArtifactRequest

ids = [f"{n:08d}-{n:04d}-4{n:03d}-8{n:03d}-{n:012d}" for n in range(1, 7)]
env = dict(
    zip(
        (
            "GREGALE_CUSTOMER_OPERATION_ID",
            "GREGALE_RUN_ID",
            "GREGALE_CUSTOMER_OPERATION_JOB_INSTANCE_ID",
            "GREGALE_CUSTOMER_OPERATION_ACCOUNT_ID",
            "GREGALE_CUSTOMER_OPERATION_APP_ID",
            "GREGALE_CUSTOMER_OPERATION_PLATFORM_TENANT_ID",
        ),
        ids,
    )
)
env.update(
    GREGALE_CUSTOMER_OPERATION_SCOPE="production",
    GREGALE_CUSTOMER_OPERATION_GENERATION="1",
    GREGALE_TASK_ATTEMPT="1",
    GREGALE_TASK_INDEX="0",
    GREGALE_CUSTOMER_OPERATION_JOB_CAPABILITY="a" * 64,
    GREGALE_CUSTOMER_OPERATION_INPUT='{"count":1}',
)


def observation():
    now = datetime.now(timezone.utc)
    return dict(
        operation_id=ids[0],
        job_run_id=ids[1],
        account_id=ids[3],
        app_id=ids[4],
        platform_tenant_id=ids[5],
        scope="production",
        generation=1,
        attempt=1,
        cancellation_requested=False,
        observed_at=now.isoformat(),
        lease_expires_at=(now + timedelta(seconds=60)).isoformat(),
        deadline_at=(now + timedelta(seconds=90)).isoformat(),
        poll_after_ms=1000,
    )


def test_job_owner_and_result_receipt():
    async def run():
        calls = []

        def respond(request):
            assert "authorization" not in request.headers
            assert request.headers["X-Gregale-Operation-Job-Capability"] == "a" * 64
            calls.append(request.url.path)
            if request.method == "GET":
                return httpx.Response(200, json=observation())
            assert json.loads(request.content)["result"] == {"file": "export.csv"}
            return httpx.Response(
                200,
                json=dict(
                    id=ids[0],
                    name="export",
                    generation=1,
                    state="running",
                    latest_sequence=1,
                    cancellation_requested=False,
                    completion_delivery={"state": "awaiting_outcome", "attempts": 0},
                    created_at=datetime.now(timezone.utc).isoformat(),
                    updated_at=datetime.now(timezone.utc).isoformat(),
                    expires_at=datetime.now(timezone.utc).isoformat(),
                ),
            )

        async with httpx.AsyncClient(transport=httpx.MockTransport(respond)) as client:
            operations = GregaleJobOperations("https://api.example.test", client, env=env)
            assert operations.context.platform_tenant_id == ids[5]
            assert operations.input == {"count": 1}
            assert "a" * 64 not in repr(operations.context)
            assert (await operations.prepare_result({"file": "export.csv"})).state == "running"
        assert len(calls) == 2

    asyncio.run(run())


@pytest.mark.parametrize(
    "change", [{"cancellation_requested": True}, {"platform_tenant_id": ids[0]}, {"generation": 2}]
)
def test_stopped_task_never_prepares_result(change):
    async def run():
        writes = []

        def respond(request):
            if request.method != "GET":
                writes.append(request.url.path)
            return httpx.Response(200, json={**observation(), **change})

        async with httpx.AsyncClient(transport=httpx.MockTransport(respond)) as client:
            operations = GregaleJobOperations("https://api.example.test", client, env=env)
            with pytest.raises((ValueError, RuntimeError)):
                await operations.prepare_result({"file": "export.csv"})
        assert not writes

    asyncio.run(run())


def test_file_receipts_use_native_proof_and_replay_stable_declaration():
    async def run():
        declaration = OperationArtifactRequest.from_dict(
            dict(
                report_id="csv",
                name="export.csv",
                uri=f"obj://{ids[4]}/{ids[3]}/export.csv",
                size_bytes=3,
                sha256="sha256:" + "b" * 64,
            )
        )
        calls = []

        def respond(request):
            assert "authorization" not in request.headers
            assert request.headers["X-Gregale-Operation-Job-Capability"] == "a" * 64
            if request.method == "GET":
                return httpx.Response(200, json=observation())
            assert json.loads(request.content) == declaration.to_dict()
            calls.append(request.url.path)
            if len(calls) == 1:
                return httpx.Response(200, json={"available": False})
            artifact = {key: value for key, value in declaration.to_dict().items() if key != "report_id"}
            artifact["id"] = ids[0]
            return httpx.Response(200, json={"available": True, "artifact": artifact})

        async with httpx.AsyncClient(transport=httpx.MockTransport(respond)) as client:
            operations = GregaleJobOperations("https://api.example.test", client, env=env)
            assert (await operations.reuse_artifact(declaration)).available is False
            prepared = await operations.prepare_artifact(declaration)
            replay = await operations.reuse_artifact(declaration)
            assert prepared.available and replay.artifact.id == prepared.artifact.id
        assert [path.rsplit("/", 1)[-1] for path in calls] == ["artifact-receipts", "artifacts", "artifact-receipts"]

    asyncio.run(run())


@pytest.mark.parametrize(
    "reply",
    [
        {"available": True},
        {"available": False, "artifact": {}},
        {"available": "yes"},
        {"available": True, "artifact": {"id": ids[0], "name": "changed.csv"}},
    ],
)
def test_file_helper_rejects_inconsistent_or_changed_receipts(reply):
    async def run():
        declaration = OperationArtifactRequest.from_dict(
            dict(
                report_id="csv",
                name="export.csv",
                uri=f"obj://{ids[4]}/{ids[3]}/export.csv",
                size_bytes=3,
                sha256="sha256:" + "b" * 64,
            )
        )

        def respond(request):
            return httpx.Response(200, json=observation() if request.method == "GET" else reply)

        async with httpx.AsyncClient(transport=httpx.MockTransport(respond)) as client:
            operations = GregaleJobOperations("https://api.example.test", client, env=env)
            with pytest.raises(ValueError):
                await operations.prepare_artifact(declaration)

    asyncio.run(run())


def test_file_helper_stops_before_preparing_after_cancellation():
    async def run():
        writes = []

        def respond(request):
            if request.method != "GET":
                writes.append(request)
            return httpx.Response(200, json={**observation(), "cancellation_requested": True})

        async with httpx.AsyncClient(transport=httpx.MockTransport(respond)) as client:
            operations = GregaleJobOperations("https://api.example.test", client, env=env)
            declaration = OperationArtifactRequest.from_dict(
                dict(
                    report_id="csv",
                    name="export.csv",
                    uri=f"obj://{ids[4]}/{ids[3]}/export.csv",
                    size_bytes=3,
                    sha256="sha256:" + "b" * 64,
                )
            )
            with pytest.raises(RuntimeError):
                await operations.prepare_artifact(declaration)
        assert not writes

    asyncio.run(run())
