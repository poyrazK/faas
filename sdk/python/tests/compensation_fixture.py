import asyncio
import copy
import json
from pathlib import Path
from uuid import uuid4

import httpx

from faas_sdk import JobContext
from faas_sdk._typed_jobs import JobCheckpoint, JobItemOutcome, JobProgress
from faas_sdk.job_runtime import JobContract, job_bytes, job_hash, prepare_job_input
from faas_sdk.preview import (
    CompensationActor,
    CompensationCommand,
    CompensationSourceStage,
    PipelineCompensation,
    PipelineJob,
    PipelineJobReceipt,
    PipelineScope,
    PipelineToolReceipt,
)
from faas_sdk.preview._pipeline_compensation_protocol import SQL
from faas_sdk.preview.pipeline_compensation_state import compensation_contracts
from faas_sdk.preview.pipeline_gates import PipelineGateRecord
from faas_sdk.workflows import run_workflow_invocation

ROOT = Path(__file__).resolve().parents[1] / "request-contracts"
if not ROOT.exists():
    ROOT = Path(__file__).resolve().parents[2] / "request-contracts"
F = json.loads((ROOT / "pipeline-compensations.json").read_text())
SOURCE = json.loads((ROOT / "job-pipelines.json").read_text())
SCOPE = PipelineScope.from_dict(SOURCE["scope"])
BUSINESS = JobContract(F["business_contract"])
ROOT_CONTRACT = JobContract(SOURCE["root_contract"])
FORWARD = JobContract(SOURCE["job_contracts"]["validate"])
CONTRACTS = compensation_contracts(BUSINESS, F["name"])


class Store:
    def __init__(self):
        self.plans, self.attempts, self.runs, self.effects = {}, {}, {}, {}
        self.lose_reserve = False

    async def load(self, id):
        return copy.deepcopy(self.plans.get(id))

    async def reserve(self, p):
        self.plans.setdefault(p["identity"], copy.deepcopy(p))
        if self.lose_reserve:
            raise RuntimeError("private database")
        return self.plans[p["identity"]]

    async def attempt(self, ns, id):
        return self.attempts.get(ns + id)

    async def bind(self, ns, id, plan):
        if ns + id in self.attempts and self.attempts[ns + id] != plan:
            raise RuntimeError("conflict")
        self.attempts[ns + id] = plan
        return plan

    async def run(self, ns, id):
        return self.runs.get(ns + id)

    async def register_run(self, ns, id, request_id, plan_id):
        old = self.runs.get(ns + id)
        if (
            old
            and old["request_id"] != request_id
            or any(v["request_id"] == request_id and v["id"] != id for v in self.runs.values())
        ):
            raise RuntimeError("conflict")
        self.runs[ns + id] = dict(id=id, request_id=request_id, plan_id=plan_id)

    async def effect(self, id):
        return self.effects.get(id)


class ReceiptStore:
    def __init__(self, cls):
        self.rows = {}
        self.cls = cls

    async def load(self, id):
        return self.rows.get(id)

    async def reserve(self, intent):
        if intent["identity"] in self.rows:
            return False, self.rows[intent["identity"]]
        r = self.cls.from_dict(dict(intent=intent, checkpoint=None))
        self.rows[intent["identity"]] = r
        return True, r

    async def complete(self, intent, checkpoint):
        r = self.cls.from_dict(dict(intent=intent, checkpoint=checkpoint.to_dict()))
        self.rows[intent["identity"]] = r
        return r


class Gates:
    def __init__(self):
        self.rows = {}

    async def open(self, intent):
        if intent["id"] in self.rows:
            return self.rows[intent["id"]]
        r = PipelineGateRecord.from_dict(
            dict(intent=intent, expires_at="2026-11-17T12:00:00.000Z", decision=None, delivery="pending")
        )
        self.rows[intent["id"]] = r
        return r

    async def resolve(self, id):
        return self.rows.get(id)

    async def list(self, id, owner):
        return [r for r in self.rows.values() if r.intent["workflow_run_id"] == id and r.intent["owner_id"] == owner]

    async def decide(self, id, binding, proposal):
        old = self.rows[id]
        if old.decision:
            return old
        r = PipelineGateRecord.from_dict(
            old.to_dict() | dict(decision=proposal | dict(decided_at="2026-11-17T10:00:00.000Z"))
        )
        self.rows[id] = r
        return r


class Fixture:
    def __init__(self, **overrides):
        self.store = Store()
        self.tool_store = ReceiptStore(PipelineToolReceipt)
        self.job_store = ReceiptStore(PipelineJobReceipt)
        self.gates = Gates()
        self.owner = "alice"
        self.revoked = False
        self.execute_allowed = True
        self.source_status = "failed"
        self.job_status = "succeeded"
        self.unresolved = False
        self.lose_start = self.lose_commit = self.fail_apply = self.invalid_output = False
        self.hang_commit = False
        self.on_commit = None
        self.posts = self.applies = 0
        self.data = {"one": dict(version=1), "two": dict(version=1)}
        self.lock = asyncio.Lock()
        envelope = dict(
            version=1,
            scope=SCOPE.to_dict(),
            owner_id="alice",
            contract_sha256=ROOT_CONTRACT.sha256,
            input=SOURCE["input"],
        )
        self.records = {
            SOURCE["workflow_run_id"]: dict(
                id=SOURCE["workflow_run_id"],
                app_id=SCOPE.app_id,
                workflow_name=ROOT_CONTRACT.spec["name"],
                input=envelope,
                status="failed",
            )
        }
        fixture = self

        class SourceStore:
            async def load(self, id):
                return PipelineJobReceipt.from_dict(
                    SOURCE["validate_receipt"]
                    | dict(checkpoint=None if fixture.unresolved else SOURCE["validate_receipt"]["checkpoint"])
                )

        class SourceJob:
            def resume(self, c):
                class Handle:
                    async def status(self, **kwargs):
                        return JobProgress(
                            run_id=c.run_id,
                            status=fixture.job_status,
                            total=1,
                            succeeded=int(fixture.job_status == "succeeded"),
                            failed=int(fixture.job_status == "failed"),
                            cancelled=0,
                            running=int(fixture.job_status == "running"),
                        )

                    async def results(self, **kwargs):
                        return (
                            JobItemOutcome(
                                input_id="one",
                                index=0,
                                attempt=1,
                                outcome="succeeded",
                                result=dict(n=SOURCE["input"]["n"] + 1),
                            ),
                        )

                return Handle()

        self.jobs = {}

        class Job:
            async def submit(self, inputs, *, options):
                refs = [
                    (
                        await prepare_job_input(
                            CONTRACTS["worker"], item, JobContext(input_id=item.id, attempt=1, _check=lambda: None)
                        )
                    ).to_dict()
                    for item in inputs
                ]
                checkpoint = JobCheckpoint.from_dict(
                    dict(
                        version=1,
                        base_url=SCOPE.base_url,
                        account_id=SCOPE.account_id,
                        job_name=CONTRACTS["worker"].spec["name"],
                        job_id=SOURCE["job_id"],
                        run_id=str(uuid4()),
                        contract_sha256=CONTRACTS["worker"].sha256,
                        input_digest=job_hash(job_bytes(refs)),
                        source_run_id=None,
                        inputs=[
                            dict(
                                input_id=r["input_id"],
                                binding_sha256=job_hash(r["input_ref"].encode()),
                                source_task_index=None,
                            )
                            for r in refs
                        ],
                    )
                )
                outputs = [
                    JobItemOutcome(
                        input_id=item.id,
                        index=n,
                        attempt=1,
                        outcome="succeeded",
                        result=await fixture.worker(
                            item.value, JobContext(input_id=item.id, attempt=1, _check=lambda: None)
                        ),
                    )
                    for n, item in enumerate(inputs)
                ]

                class Handle:
                    async def status(self, **kwargs):
                        return JobProgress(
                            run_id=checkpoint.run_id,
                            status="succeeded",
                            total=len(inputs),
                            succeeded=len(inputs),
                            failed=0,
                            cancelled=0,
                            running=0,
                        )

                    async def results(self, **kwargs):
                        return tuple(outputs)

                handle = Handle()
                handle.checkpoint = checkpoint
                fixture.jobs[checkpoint.run_id] = handle
                return handle

            def resume(self, c):
                return fixture.jobs[c.run_id]

        async def authorize(id, actor, action):
            return (
                CompensationActor(actor_id=actor, owner_id=self.owner)
                if actor == "alice" and not self.revoked
                else None
            )

        async def planner(*args):
            return [CompensationCommand(**c) for c in F["commands"]]

        async def execution(*args):
            return self.execute_allowed

        self.args = (
            dict(
                name=F["name"],
                revision="v1",
                scope=SCOPE,
                source_contract=ROOT_CONTRACT,
                source_stages=dict(
                    validate=CompensationSourceStage(
                        definition=PipelineJob(contract=FORWARD, job=SourceJob()), read_only=False
                    )
                ),
                source_store=SourceStore(),
                job=PipelineJob(contract=CONTRACTS["worker"], job=Job()),
                job_store=self.job_store,
                store=self.store,
                tool_store=self.tool_store,
                gate_store=self.gates,
                token="backend",
                review_base_url="https://review.example.test/",
                required_roles=("reviewer",),
                poll_delay=0.001,
                authorize=authorize,
                plan=planner,
                authorize_execution=execution,
                httpx_args=dict(transport=httpx.MockTransport(self.send)),
            )
            | overrides
        )
        self.recovery = PipelineCompensation(BUSINESS, **self.args)

        class Pool:
            async def connect(self):
                await fixture.lock.acquire()

                class Connection:
                    def __init__(self):
                        self.data = copy.deepcopy(fixture.data)
                        self.effects = copy.deepcopy(fixture.store.effects)
                        self.save = None
                        self.released = False

                    async def release(self, discard=False):
                        if not self.released:
                            self.released = True
                            fixture.lock.release()

                    async def execute(self, sql, values):
                        return await self.query(sql, values)

                    async def query(self, sql, values):
                        if sql.startswith("BEGIN") or sql.startswith("SELECT set_config") or sql == SQL["lock"]:
                            return []
                        if sql == "SAVEPOINT compensation_business":
                            self.save = copy.deepcopy(self.data)
                            return []
                        if sql == "ROLLBACK TO SAVEPOINT compensation_business":
                            self.data = self.save
                            return []
                        if sql in ("RELEASE SAVEPOINT compensation_business", "ROLLBACK"):
                            return []
                        if sql == "COMMIT":
                            fixture.data = self.data
                            fixture.store.effects = self.effects
                            if fixture.on_commit:
                                fixture.on_commit()
                            if fixture.hang_commit:
                                await asyncio.Event().wait()
                            if fixture.lose_commit:
                                raise RuntimeError("private commit")
                            return []
                        if sql == SQL["effect"]:
                            return [self.effects[values[0]]] if values[0] in self.effects else []
                        if sql == SQL["complete"]:
                            row = dict(command_sha256=values[1], outcome=json.loads(values[2]))
                            self.effects[values[0]] = row
                            return [row]
                        if sql == "business.read":
                            return [self.data[values[0]]] if values[0] in self.data else []
                        if sql == "business.delete":
                            self.data.pop(values[0], None)
                            return []
                        if sql == "business.edit":
                            self.data[values[0]] = dict(version=99)
                            return []
                        raise AssertionError("unexpected SQL")

                return Connection()

        self.pool = Pool()

        async def allowed(*args):
            return self.execute_allowed

        async def apply(tx, input, context):
            self.applies += 1
            rows = await tx.execute("business.read", [input["row_id"]])
            if not rows:
                return dict(state="already_compensated", result=dict(deleted=False))
            if rows[0]["version"] != input["expected_version"]:
                await tx.execute("business.edit", [input["row_id"]])
                return dict(state="needs_review", result=dict(deleted=False))
            await tx.execute("business.delete", [input["row_id"]])
            if self.fail_apply:
                raise RuntimeError("private SQL")
            if self.invalid_output:
                return dict(state="compensated", result=dict(deleted="bad"))
            return dict(state="compensated", result=dict(deleted=True))

        self.worker = self.recovery.handler(pool=self.pool, authorize=allowed, apply=apply)

    async def send(self, request):
        path = request.url.path
        if path == "/v1/account":
            return httpx.Response(200, json=dict(id=SCOPE.account_id))
        if path == "/v1/apps/imports":
            return httpx.Response(200, json=dict(id=SCOPE.app_id))
        if path.endswith("/steps"):
            return httpx.Response(200, json=dict(steps=[]))
        if path.endswith("/cancel"):
            r = self.records[path.split("/")[-2]]
            r.update(status="failed", cancelled_at="2026-11-17T10:00:00Z")
            return httpx.Response(200, json=r)
        if request.method == "POST":
            self.posts += 1
            id = str(uuid4())
            r = dict(
                id=id,
                app_id=SCOPE.app_id,
                workflow_name=CONTRACTS["root"].spec["name"],
                input=json.loads(request.content),
                status="running",
            )
            self.records[id] = r
            if self.lose_start:
                raise httpx.ReadError("private lost reply")
            return httpx.Response(201, json=r)
        r = self.records.get(path.split("/")[-1])
        if r and r["id"] == SOURCE["workflow_run_id"]:
            r["status"] = self.source_status
        return httpx.Response(200 if r else 404, json=r or {})

    async def start(self):
        p = await self.recovery.plan(SOURCE["workflow_run_id"], "alice", timeout=1)
        r = await self.recovery.tool.start(p.input, str(uuid4()), "alice", timeout=1)
        return dict(plan=p, run_id=r.receipt.checkpoint.run_id, journal=[])

    async def invoke(self, ctx):
        return await run_workflow_invocation(
            self.recovery.definition,
            dict(
                protocol="gregale.workflow.code/v1",
                workflow=CONTRACTS["root"].spec["name"],
                run_id=ctx["run_id"],
                input=self.records[ctx["run_id"]]["input"],
                invocation=len(ctx["journal"]) + 1,
                journal=ctx["journal"],
            ),
        )

    def commit(self, ctx, reply, event=False):
        for op in reply["ops"]:
            saved = op | dict(
                status="succeeded" if op["kind"] == "sleep" or event and op["kind"] == "event" else op.get("status"),
                attempt=1,
            )
            if event and op["kind"] == "event":
                saved["output"] = None
            if op["seq"] == len(ctx["journal"]):
                ctx["journal"].append(saved)
            else:
                ctx["journal"][op["seq"]] = saved

    async def until_gate(self, ctx):
        for _ in range(8):
            reply = await self.invoke(ctx)
            if any(op["kind"] == "event" for op in reply["ops"]):
                return reply
            self.commit(ctx, reply)
        raise AssertionError("no gate")

    async def approve(self, ctx, choice="approved"):
        gate = next(g for g in self.gates.rows.values() if g.intent["workflow_run_id"] == ctx["run_id"])
        await self.gates.decide(
            gate.intent["id"],
            gate.intent["binding_sha256"],
            dict(state=choice, actor_id="alice", payload=dict(reason="Reviewed exact cleanup")),
        )
        self.commit(ctx, await self.invoke(ctx), event=True)

    async def finish(self, ctx):
        for _ in range(30):
            reply = await self.invoke(ctx)
            if reply["result"]["status"] == "succeeded":
                self.records[ctx["run_id"]].update(status="succeeded", output=reply["result"]["output"])
                return reply["result"]["output"]["output"]
            assert reply["result"]["status"] != "failed", reply
            self.commit(ctx, reply)
        raise AssertionError("did not finish")
