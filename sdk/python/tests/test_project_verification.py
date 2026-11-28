"""Shared verification contract, owned deployment evidence and cooperative probes."""

import asyncio
import json
from dataclasses import replace
from datetime import UTC, datetime, timedelta
from pathlib import Path

import httpx
import pytest
from faas_sdk.preview import FaaSClient as PreviewClient

from faas_sdk import (
    FaaSClient,
    ProjectVerificationOptions,
    ProjectVerificationTarget,
    VerificationCheck,
    VerificationCheckResult,
)

CONTRACT = Path(__file__).resolve().parents[1] / "request-contracts/project-verification.json"
if not CONTRACT.exists():
    CONTRACT = Path(__file__).resolve().parents[2] / "request-contracts/project-verification.json"
FIXTURE = json.loads(CONTRACT.read_text())
TARGET = ProjectVerificationTarget.from_dict(FIXTURE["target"])


def server(scenario, paths=None):
    paths = paths if paths is not None else []

    async def handler(request):
        path = request.url.path.removeprefix("/prefix")
        paths.append(path)
        assert request.method == "GET"
        assert request.headers["Authorization"] == "Bearer owner"
        workload = next((w for w in TARGET.workloads if w.slug in path or w.deployment_id in path), None)
        subject = workload and workload.slug == "shop-worker"
        if path == "/v1/projects/shop":
            data = {"slug": "shop", "id": FIXTURE["project_id"]}
        elif not workload:
            raise AssertionError(path)
        elif subject and scenario == "denied":
            return httpx.Response(403, json={"detail": "secret-value"})
        elif path.endswith("/health"):
            date = (datetime.now(UTC) - timedelta(seconds=1)).isoformat()
            data = dict(
                app_id=workload.app_id,
                scope="default",
                status="healthy",
                phase="unsupported" if subject else "serving",
                evaluated_at=date,
                metrics_as_of=date,
                valid_for_seconds=60,
                serving_deployment_ids=[workload.deployment_id],
            )
            if subject and scenario in (
                "stale",
                "stale_sample",
                "future",
                "missing_metrics",
                "old_release",
                "mixed_release",
                "unhealthy",
                "wrong_scope",
                "naive_timestamp",
                "invalid_timestamp",
                "expired_ttl",
                "health_unknown",
            ):
                data["phase"] = "serving"
            if subject and scenario == "stale":
                data["evaluated_at"] = data["metrics_as_of"] = (datetime.now(UTC) - timedelta(seconds=300)).isoformat()
            if subject and scenario == "stale_sample":
                data["metrics_as_of"] = (datetime.now(UTC) - timedelta(seconds=300)).isoformat()
            if subject and scenario == "future":
                data["evaluated_at"] = data["metrics_as_of"] = (datetime.now(UTC) + timedelta(seconds=300)).isoformat()
            if subject and scenario == "missing_metrics":
                del data["metrics_as_of"]
            if subject and scenario == "old_release":
                data["serving_deployment_ids"] = ["c" * 32]
            if subject and scenario == "mixed_release":
                data["serving_deployment_ids"].append("c" * 32)
            if subject and scenario == "unhealthy":
                data["status"] = "unhealthy"
            if subject and scenario == "wrong_scope":
                data["scope"] = "staging"
            if subject and scenario == "naive_timestamp":
                data["evaluated_at"] = data["metrics_as_of"] = datetime.now().isoformat()
            if subject and scenario == "invalid_timestamp":
                data["evaluated_at"] = data["metrics_as_of"] = "2026-02-30T00:00:00Z"
            if subject and scenario == "expired_ttl":
                data["valid_for_seconds"] = 0
            if subject and scenario == "health_unknown":
                data["status"] = "unknown"
        elif path.endswith("/timeline"):
            data = dict(
                deployment_id=workload.deployment_id,
                app=workload.slug,
                phase="released",
                status="live",
                traffic_percent=100,
                events=[],
            )
            if subject and scenario == "release_failed":
                data.update(phase="failed", reason="secret-value")
            if subject and scenario == "approval":
                data["phase"] = "awaiting_approval"
            if subject and scenario in ("building", "superseded"):
                data["phase"] = scenario
            if subject and scenario == "unknown_phase":
                data["phase"] = "future-phase"
            if subject and scenario == "partial_traffic":
                data["traffic_percent"] = 30
            if subject and scenario == "wrong_timeline":
                data["deployment_id"] = "c" * 32
        elif path.startswith("/v1/deployments/"):
            data = dict(
                id=workload.deployment_id,
                app_id=workload.app_id,
                status="live",
                error_relevant_logs=[{"message": "secret-value " + "x" * 800}] * 25,
            )
            if subject and scenario == "wrong_deployment":
                data["app_id"] = "f" * 32
            if subject and scenario == "oversized":
                data["padding"] = "x" * 300000
        else:
            data = dict(
                id=workload.app_id,
                slug=workload.slug,
                project_id=FIXTURE["project_id"],
                workload_class="worker" if subject else "http",
            )
            if subject and scenario == "wrong_app":
                data["id"] = "f" * 32
            if subject and scenario == "wrong_project":
                data["project_id"] = "f" * 32
        return httpx.Response(
            200, json=data, headers={"X-Request-ID": "00000000-0000-4000-8000-" + str(len(paths)).zfill(12)}
        )

    return handler


def checks(scenario, calls=None):
    calls = calls if calls is not None else []

    def probe(context):
        calls.append(context.slug)
        assert context.remaining > 0
        if context.slug == "shop-worker" and scenario == "probe_exception":
            raise RuntimeError("secret-value")
        return VerificationCheckResult(
            "failed" if context.slug == "shop-worker" and scenario == "failed_worker" else "verified",
            "c" * 32 if context.slug == "shop-worker" and scenario == "unattributed" else context.deployment_id,
        )

    return tuple(
        VerificationCheck(w.slug, w.slug, probe)
        for w in TARGET.workloads
        if not (scenario == "unsupported_no_check" and w.slug == "shop-worker")
    )


@pytest.mark.parametrize("factory", [FaaSClient, PreviewClient], ids=["public", "preview"])
@pytest.mark.parametrize("case", FIXTURE["cases"], ids=lambda c: c["scenario"])
async def test_shared_verification(factory, case):
    scenario, paths, calls = case["scenario"], [], []
    target = (
        replace(TARGET, workloads=(TARGET.workloads[0], replace(TARGET.workloads[1], deployment_id=None)))
        if scenario == "unchanged"
        else TARGET
    )
    client = factory(
        base_url=TARGET.base_url, token="owner", httpx_args={"transport": httpx.MockTransport(server(scenario, paths))}
    )
    try:
        result = await client.verify_project_application(
            target,
            options=ProjectVerificationOptions(
                timeout=2,
                max_polls=2,
                poll_delay=0,
                checks=checks(scenario, calls),
                redact=lambda text: text.replace("secret-value", "[redacted]"),
            ),
        )
        assert result.workloads[0].request_ids
        assert result.outcome == case["outcome"]
        assert result.workloads[1].code == case["code"]
        assert result.target.to_dict() == target.to_dict()
        assert "secret-value" not in json.dumps(result.to_dict())
        assert all(len(w.diagnostics) <= 20 and all(len(t) <= 512 for t in w.diagnostics) for w in result.workloads)
        if scenario in (
            "denied",
            "wrong_app",
            "wrong_project",
            "wrong_deployment",
            "wrong_timeline",
            "wrong_scope",
            "oversized",
            "unchanged",
        ):
            assert "shop-worker" not in calls
        if scenario == "release_failed":
            assert result.workloads[1].diagnostics_truncated
        assert all("c" * 32 not in path for path in paths)
    finally:
        await client.aclose()
        client.close()


async def test_deadline_cancellation_limits_and_late_callbacks():
    client = FaaSClient(
        base_url=TARGET.base_url, token="owner", httpx_args={"transport": httpx.MockTransport(server("verified"))}
    )
    try:
        options = ProjectVerificationOptions(timeout=1, max_polls=1, poll_delay=0, checks=checks("verified"))
        limit = await client.verify_project_application(TARGET, options=replace(options, max_requests=2))
        assert limit.requests == 2 and limit.outcome == "inconclusive"
        entered = asyncio.Event()

        async def slow(_):
            entered.set()
            await asyncio.Future()

        slow_check = VerificationCheck("slow", "shop-worker", slow)
        timed = await client.verify_project_application(
            TARGET, options=replace(options, timeout=0.05, checks=(checks("verified")[0], slow_check))
        )
        assert timed.code == "deadline" and timed.workloads[0].outcome == "verified"
        entered.clear()
        task = asyncio.create_task(
            client.verify_project_application(TARGET, options=replace(options, checks=(slow_check,)))
        )
        await entered.wait()
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        timed = await client.verify_project_application(
            TARGET, options=replace(options, timeout=0.05, on_progress=slow)
        )
        assert timed.code == "deadline"
    finally:
        await client.aclose()
        client.close()


async def test_accounts_snapshot_and_probe_isolation():
    handler = server("verified")
    seen, active, peak = [], 0, 0

    async def transport(request):
        nonlocal active, peak
        token = request.headers["Authorization"]
        seen.append(token)
        active += 1
        peak = max(peak, active)
        await asyncio.sleep(0.002)
        active -= 1
        return await handler(request) if token == "Bearer owner" else httpx.Response(404, json={"detail": "hidden"})

    a, b = (
        FaaSClient(base_url=TARGET.base_url, token=token, httpx_args={"transport": httpx.MockTransport(transport)})
        for token in ("owner", "foreign")
    )
    try:
        pending = a.verify_project_application(
            TARGET, options=ProjectVerificationOptions(timeout=2, max_polls=1, concurrency=1, checks=checks("verified"))
        )
        a.async_httpx_client.headers["Authorization"] = "Bearer changed"
        calls = []
        first, second = await asyncio.gather(
            pending,
            b.verify_project_application(
                TARGET, options=ProjectVerificationOptions(timeout=2, max_polls=1, checks=checks("verified", calls))
            ),
        )
        assert first.outcome == "verified" and second.outcome == "inconclusive"
        assert not calls and "Bearer changed" not in seen and peak <= 2
        with pytest.raises(ValueError):
            ProjectVerificationTarget.from_dict(
                {**TARGET.to_dict(), "workloads": [*TARGET.to_dict()["workloads"], TARGET.to_dict()["workloads"][0]]}
            )
    finally:
        await a.aclose()
        await b.aclose()
        a.close()
        b.close()


async def test_text_opt_in_and_redactor_failure():
    def broken(_):
        raise RuntimeError("redact failed")

    client = FaaSClient(
        base_url=TARGET.base_url, token="owner", httpx_args={"transport": httpx.MockTransport(server("release_failed"))}
    )
    try:
        for redact in (None, broken):
            result = await client.verify_project_application(
                TARGET, options=ProjectVerificationOptions(timeout=1, max_polls=1, redact=redact)
            )
            assert all(not w.diagnostics for w in result.workloads)
            assert "secret-value" not in json.dumps(result.to_dict())
    finally:
        await client.aclose()
        client.close()


async def test_stalled_stream_deadline_and_precancelled_call():
    class Slow(httpx.AsyncByteStream):
        async def __aiter__(self):
            yield b"{"
            await asyncio.Future()

    paths = []

    async def handler(request):
        paths.append(request.url.path)
        return httpx.Response(200, stream=Slow())

    client = FaaSClient(base_url=TARGET.base_url, httpx_args={"transport": httpx.MockTransport(handler)})
    try:
        result = await client.verify_project_application(TARGET, options=ProjectVerificationOptions(timeout=0.03))
        assert result.code == "deadline" and len(paths) == 1
        task = asyncio.create_task(
            client.verify_project_application(TARGET, options=ProjectVerificationOptions(timeout=1))
        )
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        assert len(paths) == 1
    finally:
        await client.aclose()
        client.close()


async def test_poll_caps_environment_and_late_callback():
    paths = []
    client = FaaSClient(
        base_url=TARGET.base_url,
        token="owner",
        httpx_args={"transport": httpx.MockTransport(server("building", paths))},
    )
    try:
        result = await client.verify_project_application(
            TARGET, options=ProjectVerificationOptions(timeout=1, max_polls=3, poll_delay=0, checks=checks("verified"))
        )
        assert result.workloads[1].polls == 3
        assert len([p for p in paths if p.endswith("b" * 32 + "/timeline")]) == 3
    finally:
        await client.aclose()
        client.close()
    original = server("verified")

    async def scoped(request):
        response = await original(request)
        if request.url.path.endswith("/health"):
            assert request.url.params["environment"] == "staging"
        return httpx.Response(200, json={**response.json(), "scope": "staging"})

    client = FaaSClient(base_url=TARGET.base_url, token="owner", httpx_args={"transport": httpx.MockTransport(scoped)})
    try:
        result = await client.verify_project_application(
            replace(TARGET, environment="staging"),
            options=ProjectVerificationOptions(timeout=1, max_polls=1, checks=checks("verified")),
        )
        assert result.outcome == "verified"
    finally:
        await client.aclose()
        client.close()
    release, entered, ended = asyncio.Event(), asyncio.Event(), asyncio.Event()
    progress = []

    async def late(context):
        entered.set()
        try:
            await release.wait()
        except asyncio.CancelledError:
            await release.wait()
        ended.set()
        return VerificationCheckResult("verified", context.deployment_id)

    client = FaaSClient(
        base_url=TARGET.base_url, token="owner", httpx_args={"transport": httpx.MockTransport(server("verified"))}
    )
    try:
        result = await client.verify_project_application(
            TARGET,
            options=ProjectVerificationOptions(
                timeout=0.05,
                max_polls=2,
                poll_delay=0,
                checks=(VerificationCheck("late", "shop-worker", late),),
                on_progress=progress.append,
            ),
        )
        assert entered.is_set() and result.code == "deadline"
        saved, count = result.to_dict(), len(progress)
        release.set()
        await asyncio.wait_for(ended.wait(), 1)
        await asyncio.sleep(0)
        assert result.to_dict() == saved and len(progress) == count
    finally:
        release.set()
        await client.aclose()
        client.close()
