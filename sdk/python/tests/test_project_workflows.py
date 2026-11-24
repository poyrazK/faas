"""Shared reviewed-plan lifecycle, sparse receipt and interrupted apply contracts."""

import asyncio
import copy
import gzip
import hashlib
import json
import shutil
from dataclasses import replace
from email import policy
from email.parser import BytesParser
from pathlib import Path

import httpx
import pytest
from faas_sdk.preview import FaaSClient as PreviewClient

from faas_sdk import (
    FaaSClient,
    ProjectApplyOptions,
    ProjectPreviewOptions,
    ProjectWorkflowError,
    ReviewedProjectPlan,
    load_project_application_state,
    project_verification_target,
)

CONTRACT = Path(__file__).resolve().parents[1] / "request-contracts/project-workflows.json"
if not CONTRACT.exists():
    CONTRACT = Path(__file__).resolve().parents[2] / "request-contracts/project-workflows.json"
FIXTURE = json.loads(CONTRACT.read_text())
IDS = ["11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"]


def test_review_fingerprints_preserve_numeric_json_across_languages():
    portable = FIXTURE["portable_plan"]
    plan = ReviewedProjectPlan.from_dict(portable)
    assert plan.fingerprint == portable["fingerprint"]
    assert ReviewedProjectPlan.from_dict(json.loads(json.dumps(plan.to_dict()))) == plan


@pytest.mark.parametrize("factory", [FaaSClient, PreviewClient], ids=["public", "preview"])
@pytest.mark.parametrize("case", FIXTURE["cases"], ids=lambda c: c["name"])
async def test_shared_project_workflow(tmp_path, factory, case):
    root = tmp_path / "repo"
    for name in ("api", "worker"):
        (root / name).mkdir(parents=True)
        (root / name / "Dockerfile").write_text("FROM scratch\n")
    (root / "compose.yaml").write_text(
        "services:\n  api: {build: ./api}\n  worker: {build: ./worker}\n  db: {image: postgres:17}\n"
    )
    scenario = case["scenario"]
    state_path = tmp_path / "apply.json"
    preview = copy.deepcopy(FIXTURE["preview"])
    if scenario == "environment":
        preview["environment"] = "staging"
    if scenario == "blocked_plan":
        preview["can_apply"] = False
    if scenario == "unchanged":
        preview["unaffected"] = [{"slug": "shop-old", "id": IDS[0], "action": "noop"}]
    scans = posts = reads = accepted = 0
    receipt, archive, paused = None, b"", False

    async def handler(request):
        nonlocal scans, posts, reads, accepted, receipt, archive
        assert request.headers["Authorization"] == "Bearer owner"
        path = request.url.path.removeprefix("/prefix")
        if path == "/v1/projects/scan":
            scans += 1
            message = BytesParser(policy=policy.default).parsebytes(
                b"Content-Type: "
                + request.headers["Content-Type"].encode()
                + b"\r\nMIME-Version: 1.0\r\n\r\n"
                + request.content
            )
            parts = {
                p.get_param("name", header="content-disposition"): p.get_payload(decode=True)
                for p in message.iter_parts()
            }
            assert parts["project_slug"] == b"shop"
            if scenario == "environment":
                assert parts["environment"] == b"staging"
                assert json.loads(parts["app_slugs"]) == {"api": "shop-api", "worker": "shop-worker"}
            archive = parts["source"]
            assert b"postgres:17" in gzip.decompress(archive)
            return httpx.Response(200, json=preview)
        if path == "/v1/projects/apply-plan":
            posts += 1
            saved = load_project_application_state(state_path)
            assert saved.stage == "applying"
            assert saved.attempt_id == request.headers["Deployment-Attempt-ID"] == request.headers["Idempotency-Key"]
            body = json.loads(request.content)
            assert body["plan_token"] == preview["plan_token"] and "source" not in body
            if scenario == "secrets":
                assert body["initial_secrets"] == {"api": {"DATABASE_URL": "private-value"}}
                assert body["approval_token"] == "private-approval"
            if scenario in ("expired", "stale", "permission"):
                status = {"expired": 410, "stale": 409, "permission": 403}[scenario]
                return httpx.Response(
                    status,
                    json={
                        "code": {"expired": "preview_expired", "stale": "plan_token_stale", "permission": "forbidden"}[
                            scenario
                        ],
                        "title": "refused",
                        "status": status,
                    },
                    headers={"Content-Type": "application/problem+json"},
                )
            if scenario == "unknown":
                raise OSError("reply missing")
            apps = [{"slug": "shop-api", "id": IDS[0]}, {"slug": "shop-worker", "id": IDS[1]}]
            builds = [
                {"slug": a["slug"], "app_id": a["id"], "deployment_id": a["id"], "build_id": a["id"]} for a in apps
            ]
            if scenario in ("partial", "interrupted", "missing_row"):
                del builds[1]["deployment_id"], builds[1]["build_id"]
            if scenario == "partial":
                builds[1]["error"] = "enqueue failed"
            if scenario == "image":
                del builds[1]["build_id"]
            if scenario == "wrong_app":
                builds[1]["app_id"] = "another"
            if scenario == "accepted_error":
                builds[1]["error"] = "source checkpoint failed"
            receipt = {
                "attempt_id": saved.attempt_id,
                "project_slug": "another" if scenario == "wrong_project" else "shop",
                "selection_complete": True,
                "status": "interrupted" if scenario == "interrupted" else "complete",
                "apply": {
                    "project_slug": "",
                    "scan_source": "",
                    "can_apply": False,
                    "plan_token": "",
                    "workloads": None,
                    "managed": None,
                    "crons": None,
                    "submission_attempt_id": saved.attempt_id,
                    "apps": [] if scenario == "empty_receipt" else apps,
                    "builds": [] if scenario == "empty_receipt" else builds,
                    "unchanged": None,
                },
            }
            if scenario == "environment":
                receipt["environment"] = "staging"
            if scenario == "wrong_receipt":
                receipt["attempt_id"] = "f" * 32
            accepted += 1
            if scenario == "lost_reply":
                raise OSError("accepted reply lost")
            return httpx.Response(
                200,
                json={
                    "project_slug": "shop",
                    "scan_source": "compose",
                    "can_apply": True,
                    "plan_token": preview["plan_token"],
                    "submission_attempt_id": saved.attempt_id,
                    "apps": apps,
                    "builds": builds,
                },
            )
        if path.startswith("/v1/project-submissions/"):
            reads += 1
            if receipt is None:
                return httpx.Response(404, json={"code": "not_found"})
            if scenario == "poll_limit" or scenario == "processing" and reads == 1:
                return httpx.Response(
                    200,
                    json={
                        **receipt,
                        "status": "processing",
                        "selection_complete": False,
                        "apply": {"project_slug": "", "scan_source": "", "can_apply": False, "plan_token": ""},
                    },
                )
            return httpx.Response(200, json=receipt)
        if path.endswith("/timeline"):
            id = path.split("/")[3]
            worker = id == IDS[1]
            phase = (
                "failed"
                if worker and scenario == "failed_release"
                else "held"
                if worker and scenario == "blocked_release"
                else "released"
            )
            return httpx.Response(
                200,
                json={
                    "deployment_id": id,
                    "app": "shop-worker" if worker else "shop-api",
                    "status": "live",
                    "phase": phase,
                    "summary": "test",
                    "traffic_percent": 100,
                    "events": [],
                    "created_at": "2026-11-14T19:00:00Z",
                },
            )
        raise AssertionError(path)

    client = factory(
        base_url="https://projects.test/prefix", token="owner", httpx_args={"transport": httpx.MockTransport(handler)}
    )

    def progress(event):
        nonlocal paused
        if not paused and (
            scenario == "restart"
            and event.phase == "recovering"
            or scenario == "before_post"
            and event.phase == "applying"
        ):
            paused = True
            raise RuntimeError("process stopped")

    try:
        plan = await client.preview_project(
            root,
            "shop",
            options=ProjectPreviewOptions(timeout=5),
            **(
                {"environment": "staging", "app_slugs": {"api": "shop-api", "worker": "shop-worker"}}
                if scenario == "environment"
                else {}
            ),
        )
        assert scans == 1 and posts == 0
        assert plan.source.sha256 == hashlib.sha256(archive).hexdigest() and plan.source.size == len(archive)
        restored = ReviewedProjectPlan.from_dict(json.loads(json.dumps(plan.to_dict())))
        assert restored == plan
        mutable = plan.preview
        mutable.project_slug = "changed"
        assert plan.preview.project_slug == "shop"
        options = ProjectApplyOptions(
            state_path=state_path,
            timeout=7,
            max_receipt_polls=1 if scenario == "poll_limit" else 3,
            min_delay=0.001,
            max_delay=0.001,
            max_polls=2,
            on_progress=progress,
            initial_secrets={"api": {"DATABASE_URL": "private-value"}} if scenario == "secrets" else None,
            approval_token="private-approval" if scenario == "secrets" else None,
        )
        result = None
        if scenario in ("restart", "before_post"):
            with pytest.raises(ProjectWorkflowError) as stopped:
                await client.apply_project_plan(restored, options=options)
            assert stopped.value.code == "progress_failed" and paused
            shutil.rmtree(root)
            if scenario == "restart":
                result = await client.resume_project_application(restored, options=options)
            else:
                with pytest.raises(ProjectWorkflowError) as recovered:
                    await client.resume_project_application(restored, options=options)
                assert recovered.value.code == "acceptance_unknown"
        elif case["outcome"] in ("succeeded", "partial", "failed", "blocked") and scenario != "blocked_plan":
            result = await client.apply_project_plan(restored, options=options)
        else:
            with pytest.raises(ProjectWorkflowError) as stopped:
                await client.apply_project_plan(restored, options=options)
            assert stopped.value.code == case["outcome"]
            if scenario == "unknown":
                before = posts
                with pytest.raises(ProjectWorkflowError) as recovered:
                    await client.resume_project_application(restored, options=options)
                assert recovered.value.code == "acceptance_unknown" and posts == before
        if result:
            target = project_verification_target(result)
            assert target.project_slug == "shop"
            assert [w.deployment_id for w in target.workloads] == [w.deployment_id for w in result.workloads]
            assert result.outcome == case["outcome"] and len(result.workloads) >= 2
            assert result.receipt.attempt_id == result.attempt_id
            if scenario == "unchanged":
                assert next(w for w in result.workloads if w.slug == "shop-old").outcome == "unchanged"
        assert posts <= 1 and accepted <= 1
        if scenario in ("blocked_plan", "before_post"):
            assert posts == 0
        saved = load_project_application_state(state_path)
        assert state_path.stat().st_mode & 0o777 == 0o600
        assert "private-" not in json.dumps(saved.to_dict()) and "private-" not in json.dumps(plan.to_dict())
    finally:
        await client.aclose()


async def test_intent_binding_cancellation_deadlines_and_journal_concurrency(tmp_path):
    root = tmp_path / "repo"
    root.mkdir()
    (root / "package.json").write_text("{}")
    calls = []

    async def handler(request):
        calls.append(request)
        return httpx.Response(200, json=FIXTURE["preview"])

    client = FaaSClient(base_url="https://projects.test", httpx_args={"transport": httpx.MockTransport(handler)})
    foreign = FaaSClient(base_url="https://foreign.test", httpx_args={"transport": httpx.MockTransport(handler)})
    try:
        plan = await client.preview_project(root, "shop", options=ProjectPreviewOptions(timeout=5))
        with pytest.raises(ProjectWorkflowError):
            ReviewedProjectPlan.from_dict({**plan.to_dict(), "project_slug": "other"})
        options = ProjectApplyOptions(state_path=tmp_path / "state.json", timeout=2)
        with pytest.raises(ProjectWorkflowError) as refused:
            await foreign.apply_project_plan(plan, options=options)
        assert refused.value.code == "state_mismatch" and len(calls) == 1
        entered = asyncio.Event()

        async def wait(event):
            entered.set()
            await asyncio.Event().wait()

        pending = asyncio.create_task(client.apply_project_plan(plan, options=replace(options, on_progress=wait)))
        await entered.wait()
        with pytest.raises(ProjectWorkflowError) as busy:
            await client.apply_project_plan(plan, options=options)
        assert busy.value.code == "busy"
        pending.cancel()
        with pytest.raises(asyncio.CancelledError):
            await pending
        with pytest.raises(ProjectWorkflowError) as timed_out:
            await client.preview_project(root, "shop", options=ProjectPreviewOptions(timeout=0.015, on_progress=wait))
        assert timed_out.value.code == "timeout" and len(calls) == 1
        with pytest.raises(ProjectWorkflowError) as timed_out:
            await client.apply_project_plan(
                plan, options=replace(options, state_path=tmp_path / "deadline.json", timeout=0.015, on_progress=wait)
            )
        assert timed_out.value.code == "timeout" and len(calls) == 1
    finally:
        await client.aclose()
        await foreign.aclose()


async def test_concurrent_accounts_isolate_plans_receipts_and_client_snapshots(tmp_path):
    root = tmp_path / "repo"
    root.mkdir()
    (root / "package.json").write_text("{}")
    receipts, mutations, entered = {}, 0, 0
    barrier = asyncio.Event()

    async def handler(request):
        nonlocal mutations
        token = request.headers["Authorization"].removeprefix("Bearer ")
        path = request.url.path
        if path == "/v1/projects/scan":
            return httpx.Response(200, json={**FIXTURE["preview"], "plan_token": "plan-" + token})
        if path == "/v1/projects/apply-plan":
            body = json.loads(request.content)
            if body["plan_token"] != "plan-" + token:
                return httpx.Response(410, json={"code": "preview_expired"})
            attempt = request.headers["Deployment-Attempt-ID"]
            mutations += 1
            applied = {
                "project_slug": "shop",
                "scan_source": "compose",
                "can_apply": True,
                "plan_token": body["plan_token"],
                "submission_attempt_id": attempt,
                "apps": [{"id": id, "slug": "shop-worker" if i else "shop-api"} for i, id in enumerate(IDS)],
                "builds": [
                    {"app_id": id, "slug": "shop-worker" if i else "shop-api", "deployment_id": id, "build_id": id}
                    for i, id in enumerate(IDS)
                ],
            }
            receipts[token + attempt] = {
                "attempt_id": attempt,
                "project_slug": "shop",
                "selection_complete": True,
                "status": "complete",
                "apply": applied,
            }
            return httpx.Response(200, json=applied)
        if path.startswith("/v1/project-submissions/"):
            receipt = receipts.get(token + path.split("/")[-1])
            return httpx.Response(200, json=receipt) if receipt else httpx.Response(404, json={"code": "not_found"})
        if path.endswith("/timeline"):
            return httpx.Response(
                200,
                json={
                    "deployment_id": path.split("/")[3],
                    "app": "shop",
                    "status": "live",
                    "phase": "released",
                    "summary": "test",
                    "traffic_percent": 100,
                    "events": [],
                    "created_at": "2026-11-14T19:00:00Z",
                },
            )
        raise AssertionError(path)

    a, b = [
        FaaSClient(
            base_url="https://projects.test", token=token, httpx_args={"transport": httpx.MockTransport(handler)}
        )
        for token in ("a", "b")
    ]
    try:
        plans = await asyncio.gather(
            *[client.preview_project(root, "shop", options=ProjectPreviewOptions(timeout=5)) for client in (a, b)]
        )
        assert plans[0].fingerprint != plans[1].fingerprint

        async def apply(client, i):
            async def progress(event):
                nonlocal entered
                if event.phase == "applying":
                    client.async_httpx_client.headers["Authorization"] = "Bearer changed"
                    entered += 1
                    if entered == 2:
                        barrier.set()
                    await barrier.wait()

            return await client.apply_project_plan(
                plans[i],
                options=ProjectApplyOptions(state_path=tmp_path / f"{i}.json", timeout=5, on_progress=progress),
            )

        results = await asyncio.gather(apply(a, 0), apply(b, 1))
        assert mutations == 2 and all(r.outcome == "succeeded" for r in results)
        b.async_httpx_client.headers["Authorization"] = "Bearer b"
        with pytest.raises(ProjectWorkflowError) as wrong:
            await b.resume_project_application(
                plans[0], options=ProjectApplyOptions(state_path=tmp_path / "0.json", timeout=5)
            )
        assert wrong.value.code == "acceptance_unknown" and mutations == 2
        with pytest.raises(ProjectWorkflowError) as wrong:
            await b.apply_project_plan(
                plans[0], options=ProjectApplyOptions(state_path=tmp_path / "foreign.json", timeout=5)
            )
        assert wrong.value.code == "preview_expired" and mutations == 2
    finally:
        await a.aclose()
        await b.aclose()
