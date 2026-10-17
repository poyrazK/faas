import asyncio
import copy
import json
from datetime import UTC, datetime, timedelta
from pathlib import Path

import httpx
import pytest

import faas_sdk as sdk
from faas_sdk.client import Client
from faas_sdk.models.binding_verification_review import BindingVerificationReview
from faas_sdk.models.project_binding_verification_review import ProjectBindingVerificationReview

TEMPLATE = json.loads((Path(__file__).resolve().parents[2] / "binding-runner-tests/creation.json").read_text())


def fixture(graph):
    f = copy.deepcopy(TEMPLATE)
    created = datetime.now(UTC) - timedelta(seconds=1)
    expires = created + timedelta(seconds=60)
    for key in ["app_run", "graph_run"]:
        f[key]["created_at"], f[key]["expires_at"] = created.isoformat(), expires.isoformat()
    for key in ["app_review", "graph_review"]:
        f[key]["reviewed_at"] = created.isoformat()
    for name, member in f["graph_run"]["members"].items():
        member["created_at"], member["expires_at"] = created.isoformat(), expires.isoformat()
        f["graph_review"]["members"][name]["reviewed_at"] = created.isoformat()
    run = f["graph_run" if graph else "app_run"]
    return f, run, dict(run_id=run["id"], ttl_seconds=60, queue_publish=True)


def operations(client, f, graph, asynchronous, options):
    review = (ProjectBindingVerificationReview if graph else BindingVerificationReview).from_dict(
        f["graph_review" if graph else "app_review"]
    )
    names = (
        ("create_project_binding_verification_from_review", "reconcile_project_binding_verification_creation")
        if graph
        else ("create_binding_verification_from_review", "reconcile_binding_verification_creation")
    )
    args = (client, "project", "production", review) if graph else (client, "api", review)
    return [lambda name=name: getattr(sdk, ("a" if asynchronous else "") + name)(*args, **options) for name in names]


@pytest.mark.parametrize("graph", [False, True])
@pytest.mark.parametrize("asynchronous", [False, True])
@pytest.mark.parametrize(
    "fault",
    [
        "lost",
        "uuid",
        "consent",
        "ready",
        "digest",
        "scope",
        "member",
        "ttl",
        "budget",
        "plan",
        "deadline",
        "graph_parent",
    ],
)
def test_reviewed_creation(graph, asynchronous, fault):
    f, run, options = fixture(graph)
    local = fault not in ["lost", "member", "plan", "deadline", "graph_parent"]
    if fault == "uuid":
        options["run_id"] = "bad"
    elif fault == "consent":
        options["queue_publish"] = False
    elif fault == "ready":
        f["app_review"]["ready"] = f["graph_review"]["ready"] = False
    elif fault == "digest":
        f["app_review"]["review_digest"] = f["graph_review"]["review_digest"] = "bad"
    elif fault == "scope":
        f["app_review"]["scope"] = f["graph_review"]["environment"] = "other"
    elif fault == "member":
        f["app_run"]["deployment_id"] = "33333333-3333-4333-8333-333333333333"
        del f["graph_run"]["members"]["billing"]
    elif fault == "ttl":
        options["ttl_seconds"] = 0
    elif fault == "budget":
        options["max_concurrency"] = 9
    elif fault == "plan":
        f["app_run"]["steps"] = f["graph_run"]["members"]["api"]["steps"] = []
    elif fault == "deadline":
        run["expires_at"] = (datetime.fromisoformat(run["expires_at"]) + timedelta(seconds=1)).isoformat()
    elif fault == "graph_parent":
        (f["graph_run"]["members"]["api"] if graph else f["app_run"])["graph_run_id"] = (
            "33333333-3333-4333-8333-333333333333"
        )
    calls = []
    original = ValueError("create response lost")

    def handler(request):
        calls.append(request.method)
        if request.method == "POST":
            body = json.loads(request.content)
            assert body["id"] == options["run_id"]
            assert body["review_digest"] == run["review_digest"]
            assert body["queue_publish"] is True
            if graph:
                assert body["review_digests"] == f["graph_review"]["review_digests"]
            if fault == "lost":
                raise original
        return httpx.Response(200 if request.method == "GET" else 201, json=run)

    client = Client(base_url="https://api.invalid")
    client.set_httpx_client(httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handler)))
    client.set_async_httpx_client(
        httpx.AsyncClient(base_url="https://api.invalid", transport=httpx.MockTransport(handler))
    )
    create, reconcile = operations(client, f, graph, asynchronous, options)

    async def execute():
        with pytest.raises(sdk.BindingVerificationCreationError) as caught:
            await create()
        assert caught.value.attempted == (not local)
        assert caught.value.run_id == options["run_id"]
        if fault == "lost":
            assert caught.value.cause is original and caught.value.__cause__ is original
            assert str((await reconcile()).id) == options["run_id"]
        await client.get_async_httpx_client().aclose()

    if asynchronous:
        asyncio.run(execute())
    else:
        with pytest.raises(sdk.BindingVerificationCreationError) as caught:
            create()
        assert caught.value.attempted == (not local)
        assert caught.value.run_id == options["run_id"]
        if fault == "lost":
            assert caught.value.cause is original and caught.value.__cause__ is original
            assert str(reconcile().id) == options["run_id"]
    client.get_httpx_client().close()
    assert calls == ([] if local else ["POST", "GET"] if fault == "lost" else ["POST"])


def test_async_cancel_preserves_creation_id():
    f, run, options = fixture(False)

    async def execute():
        started = asyncio.Event()

        async def handler(_request):
            started.set()
            await asyncio.Event().wait()

        client = Client(base_url="https://api.invalid")
        client.set_async_httpx_client(
            httpx.AsyncClient(base_url="https://api.invalid", transport=httpx.MockTransport(handler))
        )
        create, _ = operations(client, f, False, True, options)
        task = asyncio.create_task(create())
        await started.wait()
        task.cancel()
        with pytest.raises(asyncio.CancelledError) as caught:
            await task
        assert caught.value.run_id == options["run_id"] and caught.value.attempted
        await client.get_async_httpx_client().aclose()

    asyncio.run(execute())


@pytest.mark.parametrize("graph", [False, True])
@pytest.mark.parametrize("asynchronous", [False, True])
def test_reconciliation_terminal_and_mismatch(graph, asynchronous):
    f, run, options = fixture(graph)
    past = datetime.now(UTC) - timedelta(hours=1)
    run["created_at"], run["expires_at"], run["status"] = (
        past.isoformat(),
        (past + timedelta(seconds=60)).isoformat(),
        "cancelled",
    )
    if graph:
        for member in run["members"].values():
            member["created_at"], member["expires_at"], member["status"] = (
                run["created_at"],
                run["expires_at"],
                "cancelled",
            )
    calls = []

    def handler(request):
        calls.append(request.method)
        return httpx.Response(200, json=run)

    client = Client(base_url="https://api.invalid")
    client.set_httpx_client(httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handler)))
    client.set_async_httpx_client(
        httpx.AsyncClient(base_url="https://api.invalid", transport=httpx.MockTransport(handler))
    )
    _, reconcile = operations(client, f, graph, asynchronous, options)

    async def execute():
        assert (await reconcile()).status == "cancelled"
        run["review_digest"] = "changed"
        with pytest.raises(sdk.BindingVerificationCreationError):
            await reconcile()
        await client.get_async_httpx_client().aclose()

    if asynchronous:
        asyncio.run(execute())
    else:
        assert reconcile().status == "cancelled"
        run["review_digest"] = "changed"
        with pytest.raises(sdk.BindingVerificationCreationError):
            reconcile()
    client.get_httpx_client().close()
    assert calls == ["GET", "GET"]


@pytest.mark.parametrize("graph", [False, True])
@pytest.mark.parametrize("asynchronous", [False, True])
@pytest.mark.parametrize(
    "mode",
    [
        "committed",
        "gateway",
        "absent",
        "read_forbidden",
        "read_failed",
        "retry",
        "drift",
        "ttl",
        "consent",
        "budget",
        "expired",
        "cancelled",
        "failed_resource",
        "member",
        "passed_incomplete",
        "duplicate",
        "callback",
        "cancel",
        "deadline",
        "forbidden",
        "mutation",
    ],
)
def test_automatic_creation_reconciliation(graph, asynchronous, mode, monkeypatch):
    import threading
    import time

    import faas_sdk._binding_creation as creation

    f, run, options = fixture(graph)
    for receipt, review in [
        (f["app_run"], f["app_review"]),
        *[(m, f["graph_review"]["members"][n]) for n, m in f["graph_run"]["members"].items()],
    ]:
        for i, step in enumerate(receipt["steps"]):
            if step["plan"]["action"] == "task":
                step["plan"]["action"] = "probe"
            review["plan"][i] = copy.deepcopy(step["plan"])
    stop = asyncio.Event() if asynchronous else threading.Event()
    original = httpx.ReadError("hidden-create-secret")
    events, calls = [], []
    options.update(status_read_retries=1 if mode == "retry" else 0, stop_event=stop)
    if mode == "deadline":
        monkeypatch.setattr(creation, "_CREATION_RECOVERY_SECONDS", 0.02)
    members = list(run["members"].values()) if graph else [run]
    if mode == "drift":
        run["review_digest"] = "changed"
    if mode == "ttl":
        run["expires_at"] = (datetime.fromisoformat(run["expires_at"]) + timedelta(seconds=1)).isoformat()
    if mode == "consent":
        run["queue_publish"] = False
    if mode == "budget":
        run["max_concurrency"] = 8
    if mode == "cancelled":
        run["status"] = "cancelled"
    if mode == "expired":
        at = datetime.now(UTC) - timedelta(seconds=120)
        for m in [run, *members]:
            m.update(created_at=at.isoformat(), expires_at=(at + timedelta(seconds=60)).isoformat())
    if mode == "failed_resource":
        members[0]["steps"][0].update(
            owned=True, resource_id="55555555-5555-4555-8555-000000000001", resource_kind="task", status="failed"
        )
    if mode == "passed_incomplete":
        for m in [run, *members]:
            m["status"] = "passed"
    if mode == "member":
        if graph:
            del run["members"]["billing"]
        else:
            run["graph_run_id"] = "unexpected"
    if mode == "duplicate":
        if graph:
            run["members"]["billing"]["id"] = run["members"]["api"]["id"]
        else:
            run["id"] = "55555555-5555-4555-8555-000000000001"

    def progress(event):
        events.append(event)
        if event.status == "reconciling":
            if mode == "callback":
                raise ValueError("checkpoint failed")
            if mode == "cancel":
                stop.set()

    options["on_progress"] = progress

    def handler(request):
        calls.append(request.method)
        if request.method == "POST":
            assert json.loads(request.content)["id"] == options["run_id"]
            if mode == "forbidden":
                return httpx.Response(403, json={"status": 403})
            if mode == "gateway":
                return httpx.Response(503, json={"status": 503})
            raise original
        assert request.method == "GET"
        reads = calls.count("GET")
        if mode == "deadline":
            time.sleep(0.03)
        if mode == "absent" or mode == "read_forbidden" or mode == "read_failed" or mode == "retry" and reads == 1:
            return httpx.Response(404 if mode == "absent" else 403 if mode == "read_forbidden" else 503, json={})
        return httpx.Response(200, json={**run, "private_output": "hidden-create-secret"})

    success = mode in ("committed", "gateway", "retry", "mutation")

    def check_failure(error):
        assert error.run_id == options["run_id"] and error.attempted
        if mode != "forbidden":
            assert error.cause is original

    if asynchronous:

        async def execute():
            async def async_handler(request):
                if request.method == "GET" and mode == "deadline":
                    await asyncio.sleep(0.1)
                return handler(request)

            async with httpx.AsyncClient(
                base_url="https://api.invalid", transport=httpx.MockTransport(async_handler)
            ) as transport:
                client = Client(base_url="https://api.invalid").set_async_httpx_client(transport)
                create, _ = operations(client, f, graph, True, options)
                if success:
                    assert str((await create()).id) == options["run_id"]
                else:
                    with pytest.raises(sdk.BindingVerificationCreationError) as caught:
                        await create()
                    check_failure(caught.value)

        asyncio.run(execute())
    else:
        with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handler)) as transport:
            client = Client(base_url="https://api.invalid").set_httpx_client(transport)
            create, _ = operations(client, f, graph, False, options)
            if success:
                assert str(create().id) == options["run_id"]
            else:
                with pytest.raises(sdk.BindingVerificationCreationError) as caught:
                    create()
                check_failure(caught.value)
    assert calls.count("POST") == 1
    if mode in ("callback", "cancel", "forbidden"):
        assert calls.count("GET") == 0
    if mode == "read_failed":
        assert calls.count("GET") == 1
    if mode == "retry":
        assert calls.count("GET") == 2 and events[-1].status == "retrying"
    assert bool(events) == (mode != "forbidden")
    if events:
        assert events[0].operation == "creation"
    assert "hidden-create-secret" not in str(events)


@pytest.mark.parametrize("graph", [False, True])
def test_native_async_cancel_during_creation_recovery(graph):
    f, run, options = fixture(graph)
    original = httpx.ReadError("response lost")
    calls = []

    async def execute():
        started = asyncio.Event()

        async def handle(request):
            calls.append(request.method)
            if request.method == "POST":
                raise original
            started.set()
            await asyncio.Event().wait()

        async with httpx.AsyncClient(
            base_url="https://api.invalid", transport=httpx.MockTransport(handle)
        ) as transport:
            client = Client(base_url="https://api.invalid").set_async_httpx_client(transport)
            create, _ = operations(client, f, graph, True, options)
            task = asyncio.create_task(create())
            await asyncio.wait_for(started.wait(), 1)
            task.cancel()
            with pytest.raises(asyncio.CancelledError) as caught:
                await task
            assert caught.value.run_id == options["run_id"] and caught.value.attempted
            assert caught.value.__cause__ is original

    asyncio.run(execute())
    assert calls == ["POST", "GET"]


@pytest.mark.parametrize("graph", [False, True])
@pytest.mark.parametrize("asynchronous", [False, True])
def test_creation_retry_validation_before_io(graph, asynchronous):
    f, run, options = fixture(graph)
    options["status_read_retries"] = 9
    calls = []

    def handle(request):
        calls.append(request.method)
        raise AssertionError("unexpected I/O")

    if asynchronous:

        async def execute():
            async with httpx.AsyncClient(
                base_url="https://api.invalid", transport=httpx.MockTransport(handle)
            ) as transport:
                client = Client(base_url="https://api.invalid").set_async_httpx_client(transport)
                create, _ = operations(client, f, graph, True, options)
                with pytest.raises(sdk.BindingVerificationCreationError) as error:
                    await create()
                assert not error.value.attempted

        asyncio.run(execute())
    else:
        with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handle)) as transport:
            client = Client(base_url="https://api.invalid").set_httpx_client(transport)
            create, _ = operations(client, f, graph, False, options)
            with pytest.raises(sdk.BindingVerificationCreationError) as error:
                create()
            assert not error.value.attempted
    assert calls == []
