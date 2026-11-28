import asyncio
import copy
import json
from pathlib import Path

import httpx
import pytest
from faas_sdk.preview import PipelineSchedule, PipelineScheduleClient, PipelineScheduleError, PipelineScope

from faas_sdk import JobContract

ROOT = Path(__file__).resolve().parents[1] / "request-contracts"
if not ROOT.exists():
    ROOT = Path(__file__).resolve().parents[2] / "request-contracts"
F = json.loads((ROOT / "pipeline-schedules.json").read_text())
CONTRACT = JobContract(F["contract"])
SCOPE = PipelineScope.from_dict(F["scope"])


def define(**extra):
    return PipelineSchedule(CONTRACT, F["input"], scope=SCOPE, owner_id="alice", **(F["options"] | extra))


class Native:
    def __init__(self, **extra):
        self.schedule = define(**extra)
        self.records = copy.deepcopy(F["occurrences"])
        self.replays = {}
        self.calls = self.posts = 0
        self.authorized = True
        self.denied = False
        self.lost = self.hang = self.foreign = self.bad_app = self.bad_definition = self.changed = self.pages = (
            self.refuse
        ) = self.malformed = False
        self.preview = copy.deepcopy(F["previews"][0]["response"])

    async def authorize(self, actor, action):
        return actor == "alice" and self.authorized

    def client(self):
        return PipelineScheduleClient(
            self.schedule,
            deployment_id=F["deployment_id"],
            token="platform",
            authorize=self.authorize,
            httpx_args={"transport": httpx.MockTransport(self.handle)},
        )

    async def handle(self, request):
        self.calls += 1
        assert request.headers["authorization"] == "Bearer platform"
        path = request.url.path
        s = self.schedule
        if path == "/v1/account":
            return httpx.Response(200, json={"id": SCOPE.account_id})
        if path == "/v1/apps/imports":
            return httpx.Response(200, json={"id": F["deployment_id"] if self.bad_app else SCOPE.app_id})
        if path == "/v1/deployments/" + F["deployment_id"]:
            return httpx.Response(
                200,
                json=dict(
                    id=F["deployment_id"],
                    app_id=SCOPE.app_id,
                    scope="default",
                    workflows=[
                        dict(
                            name=CONTRACT.spec["name"],
                            trigger=s.trigger | ({"input": {}} if self.bad_definition else {}),
                        )
                    ],
                ),
            )
        if path.endswith("/schedules"):
            return httpx.Response(
                200,
                json=dict(
                    runtime_enabled=True,
                    schedules=[
                        dict(
                            workflow_name=CONTRACT.spec["name"],
                            deployment_id=F["run_id"] if self.changed else F["deployment_id"],
                            **s.trigger,
                        )
                    ],
                ),
            )
        if path.endswith("/preview"):
            return httpx.Response(200, json=self.preview)
        if path.endswith("/occurrences"):
            return httpx.Response(
                200,
                json=dict(
                    occurrences=[
                        r
                        | ({"replay_run_id": self.replays[r["id"]]} if r["id"] in self.replays else {})
                        | ({"app_id": F["deployment_id"]} if self.foreign else {})
                        for r in self.records
                    ],
                    **({"next_cursor": F["replay_run_id"]} if self.pages else {}),
                ),
            )
        if "/occurrences:" in path:
            preview = path.endswith("replay-preview")
            if not preview:
                self.posts += 1
            if self.denied and not preview:
                return httpx.Response(403, json={})
            ids = json.loads(request.content)["occurrence_ids"]
            outcomes = []
            for r in sorted([r for r in self.records if r["id"] in ids], key=lambda r: r["scheduled_for"]):
                outcome = (
                    "quota_full"
                    if self.refuse
                    else "not_skipped"
                    if r["status"] == "started"
                    else "already_replayed"
                    if r["id"] in self.replays
                    else "eligible"
                    if preview
                    else "replayed"
                )
                if outcome == "replayed":
                    self.replays[r["id"]] = F["replay_run_id"]
                outcomes.append(
                    dict(
                        occurrence_id=r["id"],
                        workflow_name=r["workflow_name"],
                        scheduled_for=r["scheduled_for"],
                        outcome=outcome,
                        **({"replay_run_id": self.replays[r["id"]]} if r["id"] in self.replays else {}),
                    )
                )
            if self.hang and not preview:
                await asyncio.sleep(60)
            if self.lost and not preview:
                raise RuntimeError("private network diagnostics")
            return httpx.Response(200, json={"outcomes": [] if self.malformed else outcomes})
        if path.startswith("/v1/workflows/runs/"):
            id = path.split("/")[4]
            if path.endswith("/steps"):
                return httpx.Response(200, json={"steps": []})
            row = self.records[0] if id == F["run_id"] else next(r for r in self.records if r["id"] in self.replays)
            return httpx.Response(
                200,
                json=dict(
                    id=id,
                    app_id=SCOPE.app_id,
                    deployment_id=F["deployment_id"],
                    workflow_name=CONTRACT.spec["name"],
                    scheduled_for=row["scheduled_for"],
                    status="succeeded",
                    input=s.trigger["input"],
                    output=dict(
                        version=1, scope=SCOPE.to_dict(), owner_id="alice", contract_sha256=CONTRACT.sha256, output=17
                    ),
                ),
            )
        raise AssertionError(path)


def test_shared_identity_and_immutable_trigger():
    s = define()
    assert s.identity == F["identity"] and s.trigger == F["trigger"]
    exposed = s.trigger
    exposed["input"]["owner_id"] = "bob"
    assert s.trigger["input"]["owner_id"] == "alice"
    for extra in F["invalid_options"]:
        with pytest.raises(PipelineScheduleError, match="invalid_input"):
            define(**extra)


@pytest.mark.asyncio
@pytest.mark.parametrize("case", F["previews"], ids=lambda c: c["name"])
async def test_shared_native_calendar_previews(case):
    native = Native(**case["options"])
    native.preview = copy.deepcopy(case["response"])
    async with native.client() as client:
        result = await client.preview("alice", timeout=1, at=case["response"]["observed_at"])
        assert result.to_dict() == case["response"] and native.posts == 0


@pytest.mark.asyncio
async def test_history_precision_filtering_and_pruned_runs():
    n = Native()
    del n.records[0]["run_id"]
    n.records += [
        n.records[2] | dict(id=F["deployment_id"], workflow_name="other"),
        n.records[2] | dict(id=F["run_id"], platform_tenant_id=F["replay_run_id"]),
    ]
    async with n.client() as c:
        page = await c.history("alice", timeout=1)
        assert len(page.occurrences) == 3 and page.occurrences[0].scheduled_for == F["occurrences"][0]["scheduled_for"]
        with pytest.raises(PipelineScheduleError, match="not_found"):
            await c.attach(n.records[0]["id"], "alice", timeout=1)
        assert n.posts == 0


@pytest.mark.asyncio
async def test_preview_concurrent_duplicate_replays_and_quota():
    n = Native()
    id = n.records[1]["id"]
    async with n.client() as a, n.client() as b:
        assert (await a.preview_replay([id], "alice", timeout=1))[0].outcome == "eligible" and n.posts == 0
        result = await asyncio.gather(a.replay([id], "alice", timeout=1), b.replay([id], "alice", timeout=1))
        assert sorted(r[0].outcome for r in result) == ["already_replayed", "replayed"]
        n.refuse = True
        assert (await a.replay([n.records[2]["id"]], "alice", timeout=1))[0].outcome == "quota_full"


@pytest.mark.asyncio
async def test_replay_orders_adjacent_nanoseconds():
    n = Native()
    ids = [r["id"] for r in n.records[1:]]
    async with n.client() as c:
        assert [r.occurrence_id for r in await c.preview_replay(ids, "alice", timeout=1)] == ids[::-1]


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "mode,code",
    [
        ("authorized", "unauthorized"),
        ("bad_app", "scope_mismatch"),
        ("changed", "deployment_changed"),
        ("bad_definition", "definition_changed"),
    ],
)
async def test_auth_and_scope_before_mutation(mode, code):
    n = Native()
    setattr(n, mode, mode != "authorized")
    async with n.client() as c:
        with pytest.raises(PipelineScheduleError, match=code):
            await c.replay([n.records[1]["id"]], "alice", timeout=1)
        assert n.posts == 0
        if mode == "authorized":
            assert n.calls == 0


@pytest.mark.asyncio
async def test_bounded_selection_and_history():
    n = Native()
    async with n.client() as c:
        for ids in [[F["run_id"]] * 21, [F["run_id"]] * 2]:
            with pytest.raises(PipelineScheduleError, match="invalid_input"):
                await c.replay(ids, "alice", timeout=1)
        n.pages = True
        with pytest.raises(PipelineScheduleError, match="history_limit"):
            await c.replay([F["run_id"]], "alice", timeout=1, max_pages=1)
        assert n.posts == 0


@pytest.mark.asyncio
@pytest.mark.parametrize("mode", ["lost", "malformed", "deadline"])
async def test_unknown_replay_retains_ids_and_never_retries(mode):
    n = Native()
    n.lost = mode == "lost"
    n.malformed = mode == "malformed"
    n.hang = mode == "deadline"
    id = n.records[1]["id"]
    async with n.client() as c:
        with pytest.raises(PipelineScheduleError, match="replay_unknown") as caught:
            await c.replay([id], "alice", timeout=0.02 if mode == "deadline" else 1)
        assert caught.value.occurrence_ids == (id,) and n.posts == 1 and "private" not in str(caught.value)
        n.lost = n.malformed = n.hang = False
        assert (await c.history("alice", timeout=1)).occurrences[1].replay_run_id == F["replay_run_id"]
        assert await (await c.attach(id, "alice", timeout=1, replayed=True)).result(timeout=1) == 17


@pytest.mark.asyncio
async def test_attached_reads_and_cancel_reauthorize():
    n = Native()
    async with n.client() as c:
        run = await c.attach(n.records[0]["id"], "alice", timeout=1)
        assert await run.result(timeout=1) == 17
        n.authorized = False
        before = n.calls
        with pytest.raises(PipelineScheduleError, match="unauthorized"):
            await run.result(timeout=1)
        with pytest.raises(PipelineScheduleError, match="unauthorized"):
            await run.cancel(timeout=1)
        assert n.calls == before


@pytest.mark.asyncio
@pytest.mark.parametrize("cancel", [False, True])
async def test_deadline_cancel_fence_late_authorization(cancel):
    n = Native()
    started = asyncio.Event()

    async def authorize(actor, action):
        started.set()
        await asyncio.sleep(60)
        return True

    n.authorize = authorize
    async with n.client() as c:
        task = asyncio.create_task(c.replay([n.records[1]["id"]], "alice", timeout=1 if cancel else 0.01))
        await started.wait()
        if cancel:
            task.cancel()
        with pytest.raises(asyncio.CancelledError if cancel else PipelineScheduleError):
            await task
        assert n.calls == 0


@pytest.mark.asyncio
async def test_wrong_local_instant_and_foreign_history_fail_closed():
    n = Native()
    n.preview["upcoming"][0]["local_time"] = "2026-03-08T04:00:00-04:00"
    async with n.client() as c:
        with pytest.raises(PipelineScheduleError, match="invalid_response"):
            await c.preview("alice", timeout=1)
        n.foreign = True
        with pytest.raises(PipelineScheduleError, match="invalid_response"):
            await c.history("alice", timeout=1)


@pytest.mark.asyncio
async def test_native_write_rejection_is_explicit_without_replay():
    n = Native()
    n.denied = True
    async with n.client() as c:
        with pytest.raises(PipelineScheduleError, match="request_failed"):
            await c.replay([n.records[1]["id"]], "alice", timeout=1)
    assert n.posts == 1 and not n.replays


@pytest.mark.asyncio
async def test_historical_attachment_survives_rollout_but_replay_stays_pinned():
    n = Native()
    n.changed = True
    async with n.client() as c:
        assert len((await c.history("alice", timeout=1)).occurrences) == 3
        assert await (await c.attach(n.records[0]["id"], "alice", timeout=1)).result(timeout=1) == 17
        with pytest.raises(PipelineScheduleError, match="deployment_changed"):
            await c.replay([n.records[1]["id"]], "alice", timeout=1)
    assert n.posts == 0


def test_mismatched_owner_cannot_reuse_another_owners_deployed_input():
    n = Native()
    n.schedule._owner_id = "bob"
    with pytest.raises(PipelineScheduleError, match="invalid_input"):
        n.client()
