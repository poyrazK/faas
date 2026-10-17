import asyncio
import copy
import json
import threading
from datetime import UTC, datetime, timedelta
from pathlib import Path

import httpx
import pytest

from faas_sdk import (
    BindingVerificationRecoveryError,
    arun_binding_verification,
    arun_project_binding_verification,
    run_binding_verification,
    run_project_binding_verification,
)
from faas_sdk.client import Client

SCENARIOS = json.loads((Path(__file__).resolve().parents[2] / "binding-runner-tests/contracts.json").read_text())[
    "cases"
]


class Fixture:
    def __init__(self, scenario):
        self.scenario, self.admits, self.max_active, self.lost, self.calls = scenario, 0, 0, False, []
        created = datetime.now(UTC) - timedelta(seconds=121 if scenario.get("fault") == "expired" else 1)
        expires = created + timedelta(seconds=60)

        def member(app, number):
            return {
                "id": f"11111111-1111-4111-8111-{number:012d}",
                "app": app,
                "deployment_id": "22222222-2222-4222-8222-222222222222",
                "scope": "production",
                "status": "pending",
                "created_at": created.isoformat(),
                "expires_at": expires.isoformat(),
                "review_digest": "a" * 64,
                "queue_publish": True,
                "object_storage_write": False,
                "policy": {
                    "app_id": "33333333-3333-4333-8333-333333333333",
                    "scope": "production",
                    "mode": "enforce",
                    "revision": 1,
                    "max_verification_age": "5m",
                    "require_application_ack": False,
                },
                "inventory": {"app": app, "generated_at": created.isoformat(), "complete": True, "bindings": []},
                "steps": [],
            }

        a, b = member("api", 1), member("billing", 2)
        if scenario.get("compact"):
            a["deployment_id"] = a["deployment_id"].replace("-", "")
            b["deployment_id"] = b["deployment_id"].replace("-", "")
        self.root = a
        if scenario.get("graph"):
            root_id = "99999999-9999-4999-8999-999999999999"
            a["graph_run_id"] = b["graph_run_id"] = root_id
            self.root = {
                "id": root_id,
                "project": "project",
                "environment": "production",
                "status": "pending",
                "created_at": created.isoformat(),
                "expires_at": expires.isoformat(),
                "review_digest": "c" * 64,
                "queue_publish": True,
                "object_storage_write": False,
                "manifest": {
                    "project": "project",
                    "environment": "production",
                    "ttl_seconds": 60,
                    "deployments": {
                        "api": "22222222-2222-4222-8222-222222222222",
                        "billing": "22222222-2222-4222-8222-222222222222",
                    },
                    "expected_active_release_id": "",
                    "expected_fallback_deployments": {},
                    "expected_policy_revisions": {"api": 1, "billing": 1},
                    "service_targets": {"api": {}, "billing": {}},
                },
                "members": {"api": a, "billing": b},
            }
        for index, action in enumerate(scenario["actions"]):
            target = b if scenario.get("graph") and index % 2 else a
            kind = "queue" if action in ("queue-canary", "wait-queue") else "service"
            step = {
                "plan": {
                    "type": kind,
                    "name": f"binding-{index}",
                    "binding": f"binding-{index}",
                    "scope": "production",
                    "action": action,
                },
                "status": "planned",
                "owned": False,
            }
            if action in ("wait", "wait-queue"):
                step.update(
                    resource_id=f"44444444-4444-4444-8444-{index + 100:012d}",
                    resource_kind="queue" if kind == "queue" else "task",
                    status="published" if kind == "queue" else "queued",
                )
            target["steps"].append(step)

        self.diagnostic_root = copy.deepcopy(self.root)

    def diagnostics(self):
        def member(run):
            return {
                "run_id": run["id"],
                "graph_run_id": run.get("graph_run_id"),
                "app": run["app"],
                "deployment_id": run["deployment_id"],
                "scope": run["scope"],
                "status": "pending",
                "checked_at": datetime.now(UTC).isoformat(),
                "expires_at": run["expires_at"],
                "complete": False,
                "blockers": [
                    {
                        "code": "policy_changed",
                        "message": "Policy changed.",
                        "recovery": "review_new_run",
                        "private_command": "hidden-command",
                    }
                ],
                "changes": [],
                "recovery": [{"action": "review_new_run", "message": "Review a new receipt."}],
                "private_admissions": "hidden-secret",
            }

        run = self.diagnostic_root
        if "members" not in run:
            out = member(run)
            if out["graph_run_id"] is None:
                del out["graph_run_id"]
            return out
        return {
            "run_id": run["id"],
            "project": run["project"],
            "environment": run["environment"],
            "status": "pending",
            "checked_at": datetime.now(UTC).isoformat(),
            "expires_at": run["expires_at"],
            "complete": False,
            "members": {name: member(r) for name, r in run["members"].items()},
            "blockers": [],
            "changes": [],
            "recovery": [{"action": "review_new_run", "message": "Review a new receipt."}],
            "private_admissions": "hidden-secret",
        }

    def members(self):
        return list(self.root["members"].values()) if "members" in self.root else [self.root]

    def handle(self, request):
        path, fault = request.url.path, self.scenario.get("fault")
        assert request.method != "DELETE", "runner implicitly cancelled work"
        assert not path.endswith("/verification-runs"), "runner created a replacement receipt"
        result = self.root
        if path.endswith("/diagnostics"):
            self.calls.append("diagnostics")
            assert request.method == "GET"
            return httpx.Response(200, json=self.diagnostics())
        if path.endswith("/admit"):
            self.calls.append("admit")
            if (fault == "budget_once" or fault == "budget_after_admit" and self.admits == 1) and not self.lost:
                self.lost = True
                return httpx.Response(429, json={"status": 429, "code": "binding_verification_budget_full"})
            if fault == "capacity":
                return httpx.Response(429, json={"status": 429, "code": "binding_verification_run_limit"})
            self.admits += 1
            parts = path.split("/")
            target = next(m for m in self.members() if m["app"] == parts[3] and m["id"] == parts[6])
            step = target["steps"][int(parts[8])]
            assert not step.get("resource_id"), "runner repeated admission"
            step.update(
                resource_id=f"55555555-5555-4555-8555-{self.admits:012d}",
                resource_kind="queue" if step["plan"]["type"] == "queue" else "task",
                owned=True,
            )
            step["status"] = "published" if step["resource_kind"] == "queue" else "queued"
            if fault == "unknown_status":
                step["status"] = "unknown"
            self.max_active = max(
                self.max_active,
                sum(
                    bool(s.get("resource_id")) and s["status"] != "succeeded"
                    for m in self.members()
                    for s in m["steps"]
                ),
            )
            result = step
            if fault == "admit_once" and not self.lost:
                self.lost = True
                raise httpx.ReadError("admission response lost after commit", request=request)
        elif path.endswith("/complete"):
            self.calls.append("complete")
            assert all(
                not s.get("resource_id") or s["status"] == "succeeded" for m in self.members() for s in m["steps"]
            )
            self.root["status"] = "passed"
            for member in self.members():
                member["status"] = "pending" if fault == "member_pending" and member["app"] == "billing" else "passed"
            if fault == "false_complete":
                self.root["status"] = "blocked"
            if fault == "complete_once" and not self.lost:
                self.lost = True
                raise httpx.ReadError("completion response lost after commit", request=request)
        elif path.endswith("/resume"):
            self.calls.append("resume")
        elif request.method == "GET":
            self.calls.append("get")
            for member in self.members():
                for step in member["steps"]:
                    if step.get("resource_id"):
                        step["status"] = "failed" if fault == "failed" else "succeeded"
                        if fault == "resource_changed":
                            step["resource_id"] = "77777777-7777-4777-8777-777777777777"
            if fault == "selection_changed":
                target = {"billing": "22222222-2222-4222-8222-222222222222"}
                self.root["manifest"]["service_targets"]["api"] = target
                self.root["members"]["api"]["inventory"]["service_targets"] = target
            if fault == "blocked":
                self.root["status"] = "blocked"
            if fault == "digest_changed":
                self.root["review_digest"] = "b" * 64
            if fault == "deadline_changed":
                self.root["expires_at"] = (
                    datetime.fromisoformat(self.root["expires_at"]) + timedelta(seconds=1)
                ).isoformat()
                for member in self.members():
                    member["expires_at"] = self.root["expires_at"]
        else:
            raise AssertionError(f"unexpected request {request.method} {path}")
        return httpx.Response(200, json=copy.deepcopy(result))


@pytest.mark.parametrize("scenario", SCENARIOS, ids=lambda s: s["name"])
@pytest.mark.parametrize("asynchronous", [False, True], ids=["sync", "async"])
def test_shared_runner_recovery_contracts(scenario, asynchronous):
    f, events = Fixture(scenario), []
    expiry = f.root["expires_at"]
    options = {"poll_interval": 0.001, "concurrency": scenario.get("concurrency", 2), "on_progress": events.append}

    def execute(client):
        args = (
            (client, "project", "production", f.root["id"]) if scenario.get("graph") else (client, "api", f.root["id"])
        )
        fn = arun_project_binding_verification if scenario.get("graph") else arun_binding_verification
        if not asynchronous:
            fn = run_project_binding_verification if scenario.get("graph") else run_binding_verification
        return fn(*args, **options)

    def assertions(passed):
        assert passed == scenario["passed"]
        assert f.admits == scenario["admits"]
        assert f.max_active <= options["concurrency"]
        if passed:
            assert any(p.step == -1 and p.status == "passed" for p in events)

    if asynchronous:

        async def run():
            async with httpx.AsyncClient(
                base_url="https://api.invalid", transport=httpx.MockTransport(f.handle)
            ) as transport:
                client = Client(base_url="https://api.invalid").set_async_httpx_client(transport)
                try:
                    result = await execute(client)
                    passed = result.status == "passed"
                except BindingVerificationRecoveryError as error:
                    assert error.run_id == f.root["id"]
                    if scenario.get("resume"):
                        result = await execute(client)
                        passed = result.status == "passed"
                        assert f.root["expires_at"] == expiry
                    else:
                        passed = False
                assertions(passed)

        asyncio.run(run())
    else:
        with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(f.handle)) as transport:
            client = Client(base_url="https://api.invalid").set_httpx_client(transport)
            try:
                result = execute(client)
                passed = result.status == "passed"
            except BindingVerificationRecoveryError as error:
                assert error.run_id == f.root["id"]
                if scenario.get("resume"):
                    result = execute(client)
                    passed = result.status == "passed"
                    assert f.root["expires_at"] == expiry
                else:
                    passed = False
            assertions(passed)


@pytest.mark.parametrize("asynchronous", [False, True])
@pytest.mark.parametrize("callback_error", [False, True])
def test_local_stop_preserves_graph_work(asynchronous, callback_error):
    f = Fixture({"actions": ["probe", "queue-canary"], "graph": True})

    def progress(event):
        if event.resource_id:
            if callback_error:
                raise ValueError("checkpoint failed")
            stopped.set()

    if asynchronous:

        async def run():
            nonlocal stopped
            stopped = asyncio.Event()
            async with httpx.AsyncClient(
                base_url="https://api.invalid", transport=httpx.MockTransport(f.handle)
            ) as transport:
                client = Client(base_url="https://api.invalid").set_async_httpx_client(transport)
                with pytest.raises(BindingVerificationRecoveryError):
                    await arun_project_binding_verification(
                        client,
                        "project",
                        "production",
                        f.root["id"],
                        poll_interval=0.001,
                        on_progress=progress,
                        stop_event=stopped,
                    )

        stopped = None
        asyncio.run(run())
    else:
        stopped = threading.Event()
        with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(f.handle)) as transport:
            client = Client(base_url="https://api.invalid").set_httpx_client(transport)
            with pytest.raises(BindingVerificationRecoveryError):
                run_project_binding_verification(
                    client,
                    "project",
                    "production",
                    f.root["id"],
                    poll_interval=0.001,
                    on_progress=progress,
                    stop_event=stopped,
                )
    assert f.admits == 1
    assert f.root["status"] == "pending"


@pytest.mark.parametrize("task_cancel", [False, True])
def test_async_stop_during_admission_resumes_committed_resource(task_cancel):
    async def run():
        f = Fixture({"actions": ["probe"]})
        committed, stopped = asyncio.Event(), asyncio.Event()

        async def handle(request):
            result = f.handle(request)
            if request.url.path.endswith("/admit"):
                committed.set()
                await asyncio.Event().wait()
            return result

        async with httpx.AsyncClient(
            base_url="https://api.invalid", transport=httpx.MockTransport(handle)
        ) as transport:
            client = Client(base_url="https://api.invalid").set_async_httpx_client(transport)
            running = asyncio.create_task(
                arun_binding_verification(client, "api", f.root["id"], poll_interval=0.001, stop_event=stopped)
            )
            await asyncio.wait_for(committed.wait(), 1)
            if task_cancel:
                running.cancel()
                with pytest.raises(asyncio.CancelledError) as caught:
                    await running
                assert caught.value.run_id == f.root["id"]
            else:
                stopped.set()
                with pytest.raises(BindingVerificationRecoveryError) as caught:
                    await running
                assert caught.value.run_id == f.root["id"]
            expiry = f.root["expires_at"]
            resumed = await arun_binding_verification(client, "api", f.root["id"], poll_interval=0.001)
            assert resumed.status == "passed"
            assert f.admits == 1
            assert resumed.expires_at.isoformat() == expiry

    asyncio.run(run())


@pytest.mark.parametrize("asynchronous", [False, True])
@pytest.mark.parametrize("fault", ["budget_after_admit", "capacity"])
def test_budget_backpressure_with_wrapped_client(asynchronous, fault):
    from faas_sdk._rfc7807 import FaasProblemError

    from faas_sdk import FaaSClient, FaaSClientOptions, RetryOptions

    f = Fixture({"actions": ["probe", "queue-canary"], "graph": True, "fault": fault})
    client = FaaSClient(
        base_url="https://api.invalid",
        httpx_args={"transport": httpx.MockTransport(f.handle)},
        options=FaaSClientOptions(retry=RetryOptions(max_attempts=0)),
    )
    expiry = f.root["expires_at"]

    async def async_run():
        async with client.async_httpx_client:
            if fault == "capacity":
                with pytest.raises(BindingVerificationRecoveryError) as caught:
                    await arun_project_binding_verification(
                        client, "project", "production", f.root["id"], poll_interval=0.001
                    )
                assert isinstance(caught.value.cause, FaasProblemError)
            else:
                result = await arun_project_binding_verification(
                    client, "project", "production", f.root["id"], poll_interval=0.001
                )
                assert result.status == "passed"

    try:
        if asynchronous:
            asyncio.run(async_run())
        else:
            if fault == "capacity":
                with pytest.raises(BindingVerificationRecoveryError) as caught:
                    run_project_binding_verification(client, "project", "production", f.root["id"], poll_interval=0.001)
                assert isinstance(caught.value.cause, FaasProblemError)
            else:
                result = run_project_binding_verification(
                    client, "project", "production", f.root["id"], poll_interval=0.001
                )
                assert result.status == "passed"
        assert f.admits == (0 if fault == "capacity" else 2)
        assert f.root["expires_at"] == expiry
    finally:
        client.httpx_client.close()


@pytest.mark.parametrize("asynchronous", [False, True])
@pytest.mark.parametrize("graph", [False, True])
@pytest.mark.parametrize("mode", ["available", "forbidden", "wrong_run", "wrong_deadline", "wrong_member", "skipped"])
def test_automatic_diagnostics_are_scoped_and_preserve_failures(asynchronous, graph, mode):
    f = Fixture({"actions": ["probe"], "graph": graph, "fault": "failed"})
    reads = []

    def handle(request):
        if request.url.path.endswith("/diagnostics"):
            reads.append(request)
            assert request.method == "GET"
            if mode == "forbidden":
                return httpx.Response(403, json={"detail": "hidden-secret"})
            raw = f.diagnostics()
            if mode == "wrong_run":
                raw["run_id"] = "88888888-8888-4888-8888-888888888888"
            if mode == "wrong_deadline":
                raw["expires_at"] = (datetime.fromisoformat(f.root["expires_at"]) + timedelta(seconds=1)).isoformat()
            if mode == "wrong_member":
                (raw["members"]["api"] if graph else raw)["deployment_id"] = "88888888-8888-4888-8888-888888888888"
            return httpx.Response(200, json=raw)
        return f.handle(request)

    def check(error):
        assert error.run_id == f.root["id"]
        assert "did not succeed" in str(error.cause)
        assert (error.diagnostics is not None) == (mode == "available")
        if error.diagnostics is not None:
            assert error.diagnostics.complete is False
            assert "hidden-" not in json.dumps(error.diagnostics.to_dict())
        assert len(reads) == (0 if mode == "skipped" else 1)
        assert f.admits == 1

    options = {"poll_interval": 0.001, "skip_diagnostics": mode == "skipped"}
    if asynchronous:

        async def run():
            async with httpx.AsyncClient(
                base_url="https://api.invalid", transport=httpx.MockTransport(handle)
            ) as transport:
                client = Client(base_url="https://api.invalid").set_async_httpx_client(transport)
                with pytest.raises(BindingVerificationRecoveryError) as caught:
                    if graph:
                        await arun_project_binding_verification(
                            client, "project", "production", f.root["id"], **options
                        )
                    else:
                        await arun_binding_verification(client, "api", f.root["id"], **options)
                check(caught.value)

        asyncio.run(run())
    else:
        with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handle)) as transport:
            client = Client(base_url="https://api.invalid").set_httpx_client(transport)
            with pytest.raises(BindingVerificationRecoveryError) as caught:
                if graph:
                    run_project_binding_verification(client, "project", "production", f.root["id"], **options)
                else:
                    run_binding_verification(client, "api", f.root["id"], **options)
            check(caught.value)


@pytest.mark.parametrize("task_cancel", [False, True])
def test_async_cancellation_during_diagnostics_is_immediate(task_cancel):
    async def run():
        f = Fixture({"actions": ["probe"], "fault": "failed"})
        reading, stopped = asyncio.Event(), asyncio.Event()

        async def handle(request):
            if request.url.path.endswith("/diagnostics"):
                reading.set()
                await asyncio.Event().wait()
            return f.handle(request)

        async with httpx.AsyncClient(
            base_url="https://api.invalid", transport=httpx.MockTransport(handle)
        ) as transport:
            client = Client(base_url="https://api.invalid").set_async_httpx_client(transport)
            pending = asyncio.create_task(
                arun_binding_verification(client, "api", f.root["id"], poll_interval=0.001, stop_event=stopped)
            )
            await asyncio.wait_for(reading.wait(), 1)
            if task_cancel:
                pending.cancel()
                with pytest.raises(asyncio.CancelledError) as caught:
                    await asyncio.wait_for(pending, 0.3)
                assert caught.value.run_id == f.root["id"]
            else:
                stopped.set()
                with pytest.raises(BindingVerificationRecoveryError) as caught:
                    await asyncio.wait_for(pending, 0.3)
                assert "did not succeed" in str(caught.value.cause)
                assert caught.value.diagnostics is None
            assert f.admits == 1

    asyncio.run(run())


def test_sync_stop_during_diagnostics_and_total_timeout(monkeypatch):
    from faas_sdk import _binding_runner_diagnostics as helpers

    monkeypatch.setattr(helpers, "DIAGNOSTICS_TIMEOUT", 0.08)
    for stop in [False, True]:
        f = Fixture({"actions": ["probe"], "fault": "failed"})
        reading, released, stopped = threading.Event(), threading.Event(), threading.Event()

        def handle(request):
            if request.url.path.endswith("/diagnostics"):
                reading.set()
                released.wait(1)
                return httpx.Response(200, json=f.diagnostics())
            return f.handle(request)

        def interrupt():
            reading.wait(1)
            if stop:
                stopped.set()

        observer = threading.Thread(target=interrupt)
        observer.start()
        with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handle)) as transport:
            client = Client(base_url="https://api.invalid").set_httpx_client(transport)
            import time

            started = time.monotonic()
            try:
                with pytest.raises(BindingVerificationRecoveryError) as caught:
                    run_binding_verification(client, "api", f.root["id"], poll_interval=0.001, stop_event=stopped)
                assert time.monotonic() - started < 0.3
                assert "did not succeed" in str(caught.value.cause)
                assert caught.value.diagnostics is None
            finally:
                released.set()
                observer.join()


@pytest.mark.parametrize("graph", [False, True])
@pytest.mark.parametrize("asynchronous", [False, True])
@pytest.mark.parametrize(
    "mode", ["recover", "http503", "exhaust", "disable", "forbidden", "drift", "callback", "stop", "resume"]
)
def test_status_read_retries(graph, asynchronous, mode):
    f = Fixture(dict(graph=graph, actions=["probe"]))
    reads, events = [], []
    original = httpx.ReadError("connection lost")
    callback_error = ValueError("callback failed")
    stop = asyncio.Event() if asynchronous else threading.Event()

    def handler(request):
        if mode == "resume" and request.url.path.endswith("/resume"):
            raise original
        if request.method == "GET" and not request.url.path.endswith("/diagnostics"):
            reads.append(request.url.path)
            if mode == "forbidden":
                return httpx.Response(
                    403, json=dict(status=403, code="forbidden"), headers={"Content-Type": "application/problem+json"}
                )
            if mode == "http503" and len(reads) == 1:
                return httpx.Response(
                    503, json=dict(status=503, code="unavailable"), headers={"Content-Type": "application/problem+json"}
                )
            if len(reads) == 1 or mode == "exhaust":
                raise original
            response = f.handle(request)
            if mode == "drift":
                data = response.json()
                data["review_digest"] = "changed"
                return httpx.Response(200, json=data)
            return response
        return f.handle(request)

    def progress(event):
        events.append(event)
        if event.status == "retrying":
            if mode == "callback":
                raise callback_error
            if mode == "stop":
                stop.set()

    options = dict(
        poll_interval=0.001,
        status_read_retries=0 if mode == "disable" else 1,
        skip_diagnostics=True,
        stop_event=stop,
        on_progress=progress,
    )

    def check_error(error):
        assert error.run_id == f.root["id"]
        if mode in ["exhaust", "disable", "resume"]:
            assert error.cause is original
        if mode == "callback":
            assert error.cause is callback_error

    async def execute():
        async with httpx.AsyncClient(
            base_url="https://api.invalid", transport=httpx.MockTransport(handler)
        ) as transport:
            client = Client(base_url="https://api.invalid").set_async_httpx_client(transport)
            function = arun_project_binding_verification if graph else arun_binding_verification
            args = (client, "project", "production", f.root["id"]) if graph else (client, "api", f.root["id"])
            if mode in ["recover", "http503"]:
                assert (await function(*args, **options)).status == "passed"
            else:
                with pytest.raises(BindingVerificationRecoveryError) as caught:
                    await function(*args, **options)
                check_error(caught.value)

    if asynchronous:
        asyncio.run(execute())
    else:
        with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handler)) as transport:
            client = Client(base_url="https://api.invalid").set_httpx_client(transport)
            function = run_project_binding_verification if graph else run_binding_verification
            args = (client, "project", "production", f.root["id"]) if graph else (client, "api", f.root["id"])
            if mode in ["recover", "http503"]:
                assert function(*args, **options).status == "passed"
            else:
                with pytest.raises(BindingVerificationRecoveryError) as caught:
                    function(*args, **options)
                check_error(caught.value)
    assert len(reads) == (0 if mode == "resume" else 2 if mode in ["recover", "http503", "exhaust", "drift"] else 1)
    assert f.admits <= 1
    if mode in ["recover", "http503"]:
        assert any(e.status == "retrying" and e.retry_attempt == 1 for e in events)
        assert events[-1].status == "passed"


@pytest.mark.parametrize("graph", [False, True])
@pytest.mark.parametrize("asynchronous", [False, True])
def test_status_retry_original_expiry(graph, asynchronous):
    f = Fixture(dict(graph=graph, actions=["probe"]))
    expiry = (datetime.now(UTC) + timedelta(milliseconds=100)).isoformat()
    f.root["expires_at"] = expiry
    if graph:
        for member in f.root["members"].values():
            member["expires_at"] = expiry
    reads = []

    def handler(request):
        if request.method == "GET" and not request.url.path.endswith("/diagnostics"):
            reads.append(request.url.path)
            raise httpx.ReadError("connection lost")
        return f.handle(request)

    async def execute():
        async with httpx.AsyncClient(
            base_url="https://api.invalid", transport=httpx.MockTransport(handler)
        ) as transport:
            client = Client(base_url="https://api.invalid").set_async_httpx_client(transport)
            function = arun_project_binding_verification if graph else arun_binding_verification
            args = (client, "project", "production", f.root["id"]) if graph else (client, "api", f.root["id"])
            with pytest.raises(BindingVerificationRecoveryError) as caught:
                await function(*args, poll_interval=0.001, skip_diagnostics=True)
            assert isinstance(caught.value.cause, TimeoutError)

    if asynchronous:
        asyncio.run(execute())
    else:
        with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handler)) as transport:
            client = Client(base_url="https://api.invalid").set_httpx_client(transport)
            function = run_project_binding_verification if graph else run_binding_verification
            args = (client, "project", "production", f.root["id"]) if graph else (client, "api", f.root["id"])
            with pytest.raises(BindingVerificationRecoveryError) as caught:
                function(*args, poll_interval=0.001, skip_diagnostics=True)
            assert isinstance(caught.value.cause, TimeoutError)
    assert len(reads) == 1 and f.admits == 1 and f.root["expires_at"] == expiry


@pytest.mark.parametrize("graph", [False, True])
@pytest.mark.parametrize("asynchronous", [False, True])
@pytest.mark.parametrize("mode", ["settles", "foreign", "unknown", "drift", "callback", "stop", "deadline"])
def test_pending_acknowledgment(graph, asynchronous, mode):
    f = Fixture(dict(graph=graph, actions=["observe", "observe"] if graph else ["observe"]))
    members = list(f.root["members"].values()) if graph else [f.root]
    for member in members:
        member["steps"][0]["plan"].update(type="postgres", name="database", binding="DATABASE_URL", scope="production")
    if mode == "deadline":
        f.root["expires_at"] = (datetime.now(UTC) + timedelta(milliseconds=150)).isoformat()
        for member in members:
            member["expires_at"] = f.root["expires_at"]
    completed, observed = [], []
    stopped = asyncio.Event() if asynchronous else threading.Event()
    callback_error = ValueError("observer failed")

    def handler(request):
        if request.url.path.endswith("/complete"):
            completed.append(True)
            if len(completed) == 1 or mode == "deadline":
                data = copy.deepcopy(f.root)
                data["qualification"] = dict(
                    checked_at=datetime.now(UTC).isoformat(),
                    private_secret="hidden",
                    conditions=[
                        dict(
                            app="foreign" if mode == "foreign" else "billing" if graph else "api",
                            code="unrecognized" if mode == "unknown" else "application_adoption_unknown",
                            type="postgres",
                            name="database",
                            binding="DATABASE_URL",
                            scope="production",
                            private_command="hidden",
                        )
                    ],
                )
                return httpx.Response(202, json=data)
        response = f.handle(request)
        if mode == "drift" and completed and request.method == "GET":
            data = response.json()
            data["review_digest"] = "changed"
            return httpx.Response(200, json=data)
        return response

    def progress(event):
        if event.status == "waiting_for_qualification":
            observed.append(event)
            assert event.qualification.conditions[0].code == "application_adoption_unknown"
            assert "hidden" not in json.dumps(event.qualification.to_dict())
            if mode == "callback":
                raise callback_error
            if mode == "stop":
                stopped.set()

    options = dict(poll_interval=0.001, skip_diagnostics=True, stop_event=stopped, on_progress=progress)

    def check(error):
        assert error.run_id == f.root["id"]
        if mode == "callback":
            assert error.cause is callback_error
        if mode == "deadline":
            assert isinstance(error.cause, TimeoutError)

    async def execute():
        async with httpx.AsyncClient(
            base_url="https://api.invalid", transport=httpx.MockTransport(handler)
        ) as transport:
            client = Client(base_url="https://api.invalid").set_async_httpx_client(transport)
            function = arun_project_binding_verification if graph else arun_binding_verification
            args = (client, "project", "production", f.root["id"]) if graph else (client, "api", f.root["id"])
            if mode == "settles":
                assert (await function(*args, **options)).status == "passed"
            else:
                with pytest.raises(BindingVerificationRecoveryError) as caught:
                    await function(*args, **options)
                check(caught.value)

    if asynchronous:
        asyncio.run(execute())
    else:
        with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handler)) as transport:
            client = Client(base_url="https://api.invalid").set_httpx_client(transport)
            function = run_project_binding_verification if graph else run_binding_verification
            args = (client, "project", "production", f.root["id"]) if graph else (client, "api", f.root["id"])
            if mode == "settles":
                assert function(*args, **options).status == "passed"
            else:
                with pytest.raises(BindingVerificationRecoveryError) as caught:
                    function(*args, **options)
                check(caught.value)
    if mode == "settles":
        assert len(completed) == 2 and observed
    elif mode != "deadline":
        assert len(completed) == 1
    assert f.admits == 0


@pytest.mark.parametrize("graph", [False, True])
@pytest.mark.parametrize("asynchronous", [False, True])
@pytest.mark.parametrize("phase", ["admission", "completion"])
@pytest.mark.parametrize(
    "mode",
    [
        "committed",
        "gateway",
        "absent",
        "drift",
        "deadline",
        "unowned",
        "malformed",
        "read_failed",
        "retry",
        "forbidden",
        "callback",
        "cancel",
        "expires",
        "member_pending",
    ],
)
def test_uncertain_write_reconciliation(graph, asynchronous, phase, mode):
    if mode == "member_pending" and phase == "admission":
        return
    f = Fixture({"graph": graph, "actions": ["probe", "queue-canary"]})
    if mode == "expires":
        f.root["expires_at"] = (datetime.now(UTC) + timedelta(milliseconds=80)).isoformat()
        for member in f.members():
            member["expires_at"] = f.root["expires_at"]
    events, target, writes, reads, lost = [], None, 0, 0, False
    original = httpx.ReadError("response lost")
    stop = asyncio.Event() if asynchronous else threading.Event()

    def handle(request):
        nonlocal target, writes, reads, lost
        path = request.url.path
        if path == target:
            writes += 1
        if not lost and path.endswith("/admit" if phase == "admission" else "/complete"):
            target, writes, lost = path, writes + 1, True
            if mode == "forbidden":
                return httpx.Response(403, json={"status": 403, "code": "forbidden"})
            if mode != "absent":
                f.handle(request)
            if mode == "gateway":
                return httpx.Response(503, json={"status": 503})
            raise original
        if lost and request.method == "GET":
            reads += 1
            if mode == "expires" and not asynchronous:
                import time

                time.sleep(0.2)
            if mode == "read_failed" or mode == "retry" and reads == 1:
                return httpx.Response(503, json={"status": 503})
            raw = f.handle(request).json()
            members = list(raw["members"].values()) if "members" in raw else [raw]
            if mode == "drift":
                raw["review_digest"] = "d" * 64
            if mode == "deadline":
                raw["expires_at"] = (datetime.fromisoformat(raw["expires_at"]) + timedelta(seconds=1)).isoformat()
            if mode in ("unowned", "malformed"):
                for member in members:
                    for step in member["steps"]:
                        step.update(owned=False) if mode == "unowned" else step.update(resource_id="bad")
            if mode == "member_pending":
                members[-1]["status"] = "pending"
            return httpx.Response(200, json=raw)
        return f.handle(request)

    def progress(event):
        events.append(event)
        if event.status == "reconciling":
            if mode == "callback":
                raise ValueError("checkpoint failed")
            if mode == "cancel":
                stop.set()

    options = dict(
        skip_diagnostics=True,
        status_read_retries=1 if mode == "retry" else 0,
        poll_interval=0.001,
        stop_event=stop,
        on_progress=progress,
    )
    success = mode in ("committed", "gateway", "retry")

    def check_failure(error):
        assert error.run_id == f.root["id"]
        if mode in ("expires", "callback"):
            assert error.cause is original or error.cause.__cause__ is original
        elif mode not in ("forbidden", "callback", "cancel"):
            assert error.__cause__ is original

    if asynchronous:

        async def run():
            async def async_handle(request):
                if lost and mode == "expires" and request.method == "GET":
                    await asyncio.sleep(0.2)
                return handle(request)

            async with httpx.AsyncClient(
                base_url="https://api.invalid", transport=httpx.MockTransport(async_handle)
            ) as transport:
                client = Client(base_url="https://api.invalid").set_async_httpx_client(transport)
                execute = (
                    arun_project_binding_verification(client, "project", "production", f.root["id"], **options)
                    if graph
                    else arun_binding_verification(client, "api", f.root["id"], **options)
                )
                if success:
                    assert (await execute).status == "passed"
                else:
                    with pytest.raises(BindingVerificationRecoveryError) as error:
                        await execute
                    check_failure(error.value)

        asyncio.run(run())
    else:
        with httpx.Client(base_url="https://api.invalid", transport=httpx.MockTransport(handle)) as transport:
            client = Client(base_url="https://api.invalid").set_httpx_client(transport)
            execute = lambda: (
                run_project_binding_verification(client, "project", "production", f.root["id"], **options)
                if graph
                else run_binding_verification(client, "api", f.root["id"], **options)
            )
            if success:
                assert execute().status == "passed"
            else:
                with pytest.raises(BindingVerificationRecoveryError) as error:
                    execute()
                check_failure(error.value)
    assert writes == 1
    reconciliation = [e for e in events if e.status == "reconciling"]
    assert len(reconciliation) == (0 if mode == "forbidden" else 1)
    if reconciliation:
        assert reconciliation[0].operation == phase
    if mode in ("callback", "cancel", "forbidden"):
        assert reads == 0
    if success:
        assert f.admits == 2


@pytest.mark.parametrize("graph", [False, True])
@pytest.mark.parametrize("phase", ["admission", "completion"])
def test_native_async_cancel_during_write_reconciliation(graph, phase):
    f = Fixture({"graph": graph, "actions": ["probe"]})
    original = httpx.ReadError("response lost")
    writes = 0

    async def run():
        started = asyncio.Event()

        async def handle(request):
            nonlocal writes
            if request.url.path.endswith("/admit" if phase == "admission" else "/complete"):
                writes += 1
                f.handle(request)
                raise original
            if writes and request.method == "GET":
                started.set()
                await asyncio.Event().wait()
            return f.handle(request)

        async with httpx.AsyncClient(
            base_url="https://api.invalid", transport=httpx.MockTransport(handle)
        ) as transport:
            client = Client(base_url="https://api.invalid").set_async_httpx_client(transport)
            operation = (
                arun_project_binding_verification(client, "project", "production", f.root["id"], poll_interval=0.001)
                if graph
                else arun_binding_verification(client, "api", f.root["id"], poll_interval=0.001)
            )
            task = asyncio.create_task(operation)
            await asyncio.wait_for(started.wait(), 1)
            task.cancel()
            with pytest.raises(asyncio.CancelledError) as error:
                await task
            assert error.value.run_id == f.root["id"]
            assert error.value.__cause__ is original
            assert writes == 1

    asyncio.run(run())
