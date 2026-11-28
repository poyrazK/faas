"""Shared protocol plus deterministic native/provider transports for action tests."""

import asyncio
import copy
import json
from pathlib import Path
from urllib.parse import parse_qs
from uuid import uuid4

import httpx
from compensation_fixture import Gates, ReceiptStore
from faas_sdk.preview.outbound_action_state import OutboundActionActor, OutboundActionError
from faas_sdk.preview.outbound_actions import OutboundAction
from faas_sdk.preview.pipeline_state import PipelineScope
from faas_sdk.preview.pipeline_tool_state import PipelineToolReceipt
from faas_sdk.preview.stripe_refund_actions import StripeRefundActionProvider, stripe_refund_contract
from faas_sdk.workflows import run_workflow_invocation

F = json.loads((Path(__file__).resolve().parents[2] / "request-contracts/outbound-actions.json").read_text())


class Store:
    def __init__(self, fixture):
        self.fixture = fixture
        self.rows, self.runs, self.attempts = {}, {}, {}

    async def load(self, id):
        return copy.deepcopy(self.rows.get(id))

    async def reserve(self, intent):
        self.rows.setdefault(
            intent["identity"],
            dict(intent=copy.deepcopy(intent), version=0, outcome=dict(state="prepared", code="prepared")),
        )
        if self.fixture.lose_reserve:
            raise RuntimeError("private SQL error")
        return copy.deepcopy(self.rows[intent["identity"]])

    async def claim(self, id, version):
        if self.fixture.before_claim:
            await self.fixture.before_claim()
        r = self.rows[id]
        if r["version"] != version or r["outcome"]["state"] != "prepared":
            return None
        r.update(version=version + 1, outcome=dict(state="unknown", code="dispatch_unconfirmed"))
        if self.fixture.lose_claim:
            raise RuntimeError("private SQL acknowledgement lost")
        return copy.deepcopy(r)

    async def observe(self, id, version, outcome):
        r = self.rows[id]
        if r["version"] != version or r["outcome"]["state"] not in ("unknown", "pending"):
            return None
        r.update(version=version + 1, outcome=copy.deepcopy(outcome))
        if self.fixture.lose_observe:
            raise RuntimeError("private COMMIT acknowledgement lost")
        return copy.deepcopy(r)

    async def cancel(self, id, version):
        r = self.rows[id]
        if r["version"] != version or r["outcome"]["state"] != "prepared":
            return None
        r.update(version=version + 1, outcome=dict(state="cancelled", code="cancelled_before_dispatch"))
        return copy.deepcopy(r)

    async def run(self, id, run_id):
        return self.runs.get((id, run_id))

    async def register_run(self, id, run_id, digest):
        if self.runs.setdefault((id, run_id), digest) != digest:
            raise OutboundActionError("conflict")

    async def attempt(self, ns, id):
        return self.attempts.get((ns, id))

    async def bind(self, ns, id, identity):
        if self.attempts.setdefault((ns, id), identity) != identity:
            raise OutboundActionError("conflict")


class Fixture:
    def __init__(self):
        self.revoked, self.owner, self.execution_allowed = False, "alice", True
        self.lose_claim = self.lose_reserve = self.lose_observe = self.lose_response = False
        self.pending = self.bad_account = self.missing = self.duplicate = self.hang_post = False
        self.on_post = self.on_verify = self.before_claim = None
        self.posts, self.http_status = 0, 200
        self.account_padding = 0
        self.refunds, self.native_runs = [], {}
        self.store, self.gates, self.tools = Store(self), Gates(), ReceiptStore(PipelineToolReceipt)
        self.action = self.make()

    def make(self, revision="v1"):
        async def authorize(id, input, access, action):
            return (
                OutboundActionActor(actor_id=access, owner_id=self.owner)
                if (
                    not self.revoked
                    and access == self.owner
                    and input["payment_intent"] == "pi_csv"
                    and input["amount_minor"] == 1000
                )
                else None
            )

        async def execution(intent, run_id):
            return not self.revoked and self.execution_allowed and intent["owner_id"] == self.owner

        return OutboundAction(
            stripe_refund_contract,
            name="csv_refund",
            revision=revision,
            scope=PipelineScope.from_dict(F["scope"]),
            integration_id="33333333-3333-4333-8333-333333333333",
            relay_url="http://relay.test",
            token="fixture",
            store=self.store,
            provider=StripeRefundActionProvider(account_id="acct_fixture", api_version="2026-10-28.endive"),
            tool_store=self.tools,
            gate_store=self.gates,
            review_base_url="https://review.test",
            required_roles=("reviewer",),
            authorize=authorize,
            authorize_execution=execution,
            httpx_args=dict(transport=httpx.MockTransport(self.fetch)),
        )

    async def fetch(self, request):
        path = request.url.path
        if request.url.host == "relay.test":
            assert "authorization" not in request.headers
            if path.endswith("/v1/account"):
                if self.on_verify:
                    self.on_verify()
                return httpx.Response(
                    200,
                    json=dict(
                        id="acct_wrong" if self.bad_account else "acct_fixture",
                        metadata=dict(padding="x" * self.account_padding),
                    ),
                )
            if request.method == "POST":
                self.posts += 1
                if self.on_post:
                    self.on_post()
                form = {k: v[0] for k, v in parse_qs(request.content.decode()).items()}
                refund = dict(
                    id="re_" + str(self.posts),
                    object="refund",
                    payment_intent=form["payment_intent"],
                    amount=int(form["amount"]),
                    currency=form["currency"],
                    status="pending" if self.pending else "succeeded",
                    metadata=dict(
                        gregale_action_id=form["metadata[gregale_action_id]"],
                        gregale_input_sha256=form["metadata[gregale_input_sha256]"],
                    ),
                )
                if self.http_status == 200:
                    self.refunds.append(refund)
                if self.hang_post:
                    await asyncio.Event().wait()
                if self.lose_response:
                    raise httpx.ReadError("provider accepted; private transport diagnostics")
                return httpx.Response(self.http_status, json=refund)
            if path.endswith("/v1/refunds"):
                data = (
                    []
                    if self.missing
                    else [*self.refunds, self.refunds[0] | dict(id="re_duplicate")]
                    if self.duplicate
                    else self.refunds
                )
                return httpx.Response(200, json=dict(object="list", has_more=False, data=data))
            record = next((r for r in self.refunds if path.endswith("/" + r["id"])), None)
            return httpx.Response(200 if record else 404, json=record or {})
        if path == "/v1/account":
            return httpx.Response(200, json=dict(id=F["scope"]["account_id"]))
        if path == "/v1/apps/imports":
            return httpx.Response(200, json=dict(id=F["scope"]["app_id"]))
        if path.endswith("/steps"):
            return httpx.Response(200, json=dict(steps=[]))
        if request.method == "POST":
            id = str(uuid4())
            record = dict(
                id=id,
                app_id=F["scope"]["app_id"],
                workflow_name=self.action.contract.spec["name"],
                input=json.loads(request.content),
                status="running",
            )
            self.native_runs[id] = record
            return httpx.Response(201, json=record)
        record = self.native_runs.get(path.split("/")[-1])
        return httpx.Response(200 if record else 404, json=record or {})

    async def start(self, prepared):
        ref = {k: prepared[k] for k in ("action_id", "binding_sha256")}
        receipt = await self.action.tool.start(ref, str(uuid4()), "alice", timeout=1)
        return dict(run_id=receipt.receipt.checkpoint.run_id, journal=[])

    async def invoke(self, ctx):
        return await run_workflow_invocation(
            self.action.definition,
            dict(
                protocol="gregale.workflow.code/v1",
                workflow=self.action.contract.spec["name"],
                run_id=ctx["run_id"],
                input=self.native_runs[ctx["run_id"]]["input"],
                invocation=len(ctx["journal"]) + 1,
                journal=ctx["journal"],
            ),
        )

    def commit(self, ctx, reply, event=False):
        for op in reply["ops"]:
            value = op | dict(
                status="succeeded" if op["kind"] == "sleep" or event and op["kind"] == "event" else op.get("status"),
                attempt=1,
            )
            if event and op["kind"] == "event":
                value["output"] = None
            if op["seq"] == len(ctx["journal"]):
                ctx["journal"].append(value)
            else:
                ctx["journal"][op["seq"]] = value

    async def until_gate(self, ctx):
        for _ in range(10):
            reply = await self.invoke(ctx)
            assert reply["result"]["status"] != "failed", reply
            if any(op["kind"] == "event" for op in reply["ops"]):
                return reply
            self.commit(ctx, reply)
        raise AssertionError("No gate")

    async def approve(self, ctx, choice="approved"):
        gate = next(g for g in self.gates.rows.values() if g.intent["workflow_run_id"] == ctx["run_id"])
        await self.gates.decide(
            gate.intent["id"],
            gate.intent["binding_sha256"],
            dict(
                state=choice,
                actor_id=None if choice == "expired" else "alice",
                payload=None if choice in ("expired", "cancelled") else dict(reason="Approved exact refund"),
            ),
        )
        self.commit(ctx, await self.invoke(ctx), event=True)

    async def finish(self, ctx):
        for _ in range(15):
            reply = await self.invoke(ctx)
            assert reply["result"]["status"] != "failed", reply
            if reply["result"]["status"] == "succeeded":
                self.native_runs[ctx["run_id"]].update(status="succeeded", output=reply["result"]["output"])
                return reply["result"]["output"]["output"]
            self.commit(ctx, reply)
        raise AssertionError("Not finished")
