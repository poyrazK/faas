"""ADR-521: fresh runtime identity and isolated HTTP attempt authority."""

import asyncio
import json
from dataclasses import asdict
from datetime import UTC

import httpx
import pytest

from faas_sdk.models.operation_report_request import OperationReportRequest
from faas_sdk.operations_runtime import GregaleOperations, OperationHTTPError

OP = "11111111-1111-4111-8111-111111111111"
INV = "22222222-2222-4222-8222-222222222222"


def headers(operation=OP, capability="a" * 64):
    return {
        "X-Gregale-Customer-Operation-Id": operation,
        "X-Faas-Invocation-Id": INV,
        "X-Gregale-Operation-Attempt": "2",
        "X-Gregale-Operation-Capability": capability,
    }


def snapshot(operation=OP):
    return {
        "id": operation,
        "name": "export",
        "generation": 1,
        "state": "running",
        "completion_delivery": {"state": "pending", "attempts": 1},
        "cancellation_requested": False,
        "latest_sequence": 2,
        "created_at": "2026-10-04T12:00:00Z",
        "updated_at": "2026-10-04T12:00:00Z",
        "expires_at": "2026-11-04T12:00:00Z",
    }


def test_runtime_control_refreshes_identity_and_preserves_separate_cancel_intent():
    async def exercise():
        identities, reads = [], []

        def transport(request):
            if request.url.host == "127.0.0.1":
                identities.append(request)
                return httpx.Response(200, json={"access_token": f"workload-{len(identities)}"})
            reads.append(request)
            assert request.method == "GET" and not request.content
            assert request.url.path == f"/v1/runtime/operations/{OP}/control"
            assert request.headers["Authorization"] == f"Bearer workload-{len(identities)}"
            assert request.headers["X-Faas-Invocation-Id"] == INV
            assert request.headers["X-Gregale-Operation-Attempt"] == "2"
            assert request.headers["X-Gregale-Operation-Capability"] == "a" * 64
            return httpx.Response(
                200,
                json={
                    "operation_id": OP,
                    "invocation_id": INV,
                    "attempt": 2,
                    "cancellation_requested": True,
                    "observed_at": "2026-10-05T11:58:00Z",
                    "lease_expires_at": "2026-10-05T11:59:00Z",
                    "deadline_at": "2026-10-05T12:00:00Z",
                    "poll_after_ms": 1000,
                    "private_extra": "discard",
                },
            )

        async with httpx.AsyncClient(transport=httpx.MockTransport(transport)) as client:
            runtime = GregaleOperations("https://api.gregale.test", "http://127.0.0.1/identity", client)
            with runtime.bind_request(headers()):
                for _ in range(2):
                    value = await runtime.control()
                    assert value.cancellation_requested and value.lease_expires_at < value.deadline_at
                    assert value.additional_properties == {}
            assert len(identities) == len(reads) == 2
            with pytest.raises(ValueError, match="requires an operation"):
                await runtime.control()

    asyncio.run(exercise())


def test_runtime_control_rejects_replacement_attempt():
    async def exercise():
        def transport(request):
            if request.url.host == "127.0.0.1":
                return httpx.Response(200, json={"access_token": "workload"})
            return httpx.Response(
                200,
                json={
                    "operation_id": OP,
                    "invocation_id": INV,
                    "attempt": 3,
                    "cancellation_requested": False,
                    "observed_at": "2026-10-05T11:58:00Z",
                    "lease_expires_at": "2026-10-05T11:59:00Z",
                    "deadline_at": "2026-10-05T12:00:00Z",
                    "poll_after_ms": 1000,
                },
            )

        async with httpx.AsyncClient(transport=httpx.MockTransport(transport)) as client:
            runtime = GregaleOperations("https://api.gregale.test", "http://127.0.0.1/identity", client)
            with runtime.bind_request(headers()):
                with pytest.raises(ValueError, match="Invalid operation control"):
                    await runtime.control()

    asyncio.run(exercise())


def test_runtime_refresh_and_receipt_replay():
    async def exercise():
        identities, reports = [], []

        def transport(request):
            if request.url.host == "127.0.0.1":
                assert request.url.params["audience"] == "gregale:operations"
                identities.append(request)
                return httpx.Response(200, json={"access_token": f"workload-{len(identities)}"})
            reports.append(request)
            assert request.headers["X-Faas-Invocation-Id"] == INV
            assert request.headers["X-Gregale-Operation-Attempt"] == "2"
            assert request.headers["X-Gregale-Operation-Capability"] == "a" * 64
            return httpx.Response(200, json=snapshot())

        async with httpx.AsyncClient(transport=httpx.MockTransport(transport)) as client:
            runtime = GregaleOperations("https://api.gregale.test", "http://127.0.0.1/identity", client)
            with runtime.bind_request(headers()) as context:
                assert "capability" not in asdict(context)
                for _ in range(2):
                    status = await runtime.progress(OperationReportRequest("chunk-1", "generating", 10, 100))
                    assert status.completion_delivery.state == "pending"
                with runtime.bind_request({}):
                    with pytest.raises(ValueError, match="requires an operation"):
                        await runtime.progress(OperationReportRequest("chunk-1", "generating", 10, 100))
                assert runtime.context() == context
            assert runtime.context() is None
            assert len(identities) == len(reports) == 2
            assert reports[0].content == reports[1].content
            assert reports[1].headers["Authorization"] == "Bearer workload-2"

    asyncio.run(exercise())


def test_concurrent_handlers_do_not_share_authority():
    async def exercise():
        seen = {}

        def transport(request):
            if request.url.host == "127.0.0.1":
                return httpx.Response(200, json={"access_token": "workload"})
            operation = request.url.path.split("/")[-2]
            seen[operation] = request.headers["X-Gregale-Operation-Capability"]
            return httpx.Response(200, json=snapshot(operation))

        async with httpx.AsyncClient(transport=httpx.MockTransport(transport)) as client:
            runtime = GregaleOperations("https://api.gregale.test", "http://127.0.0.1/identity", client)

            async def handler(operation, capability):
                with runtime.bind_request(headers(operation, capability)):
                    await asyncio.sleep(0)
                    await runtime.progress(OperationReportRequest("chunk-1", "generating", 1, 1))

            other = "33333333-3333-4333-8333-333333333333"
            await asyncio.gather(handler(OP, "a" * 64), handler(other, "b" * 64))
            assert seen == {OP: "a" * 64, other: "b" * 64}

    asyncio.run(exercise())


def test_native_context_and_redirects_are_rejected():
    async def exercise():
        calls = []

        def transport(request):
            calls.append(request)
            return httpx.Response(307, headers={"Location": "https://outside.example/identity"})

        async with httpx.AsyncClient(transport=httpx.MockTransport(transport)) as client:
            runtime = GregaleOperations("https://api.gregale.test", "http://127.0.0.1/identity", client)
            with pytest.raises(ValueError, match="Invalid operation"):
                with runtime.bind_request({**headers(), "X-Gregale-Operation-Execution-Kind": "job"}):
                    pass
            with runtime.bind_request(headers()):
                with pytest.raises(OperationHTTPError) as failure:
                    await runtime.progress(OperationReportRequest("chunk-1", "generating", 1, 1))
                assert failure.value.status == 307
            assert len(calls) == 1

    asyncio.run(exercise())


def test_problem_does_not_turn_delivery_into_business_failure():
    async def exercise():
        def transport(request):
            if request.url.host == "127.0.0.1":
                return httpx.Response(200, json={"access_token": "workload"})
            assert json.loads(request.content)["report_id"] == "stable-report"
            return httpx.Response(409, json={"code": "operation_state_conflict"})

        async with httpx.AsyncClient(transport=httpx.MockTransport(transport)) as client:
            runtime = GregaleOperations("https://api.gregale.test", "http://127.0.0.1/identity", client)
            with runtime.bind_request(headers()):
                with pytest.raises(OperationHTTPError) as error:
                    await runtime.progress(OperationReportRequest("stable-report", "generating", 1, 1))
                assert error.value.code == "operation_state_conflict"

    asyncio.run(exercise())


def test_runtime_milestone_uses_current_proof_and_saved_identity():
    from datetime import datetime
    from uuid import UUID

    from faas_sdk.models.operation_milestone_request import OperationMilestoneRequest

    async def exercise():
        seen = []

        def transport(request):
            if request.url.host == "127.0.0.1":
                return httpx.Response(200, json={"access_token": "fresh"})
            seen.append(request)
            body = json.loads(request.content)
            return httpx.Response(
                200, json={**body, "operation_id": OP, "created_at": "2026-10-07T12:05:00Z", "sequence": 3}
            )

        async with httpx.AsyncClient(transport=httpx.MockTransport(transport)) as client:
            runtime = GregaleOperations(
                api_url="https://api.gregale.test", identity_endpoint="http://127.0.0.1/identity", client=client
            )
            with runtime.bind_request(headers()):
                fact = await runtime.milestone(
                    OperationMilestoneRequest(
                        id=UUID(OP),
                        name="paid",
                        payload={"total": 1},
                        occurred_at=datetime(2026, 10, 7, 12, tzinfo=UTC),
                    )
                )
            assert fact.id == UUID(OP)
            assert str(seen[0].url).endswith("/milestones")
            assert seen[0].headers["X-Gregale-Operation-Attempt"] == "2"
            assert seen[0].headers["X-Gregale-Operation-Capability"] == "a" * 64
            assert seen[0].headers["Authorization"] == "Bearer fresh"

    asyncio.run(exercise())
