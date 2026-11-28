import asyncio
import copy
import hashlib
import json
from pathlib import Path

import httpx
import pytest
from faas_sdk.preview import (
    EventContract,
    PipelineScope,
    StripeWebhookAdapter,
    StripeWebhookDelivery,
    StripeWebhookError,
    StripeWebhookIgnore,
    StripeWebhookStage,
    stripe_delivery_context_from_headers,
)
from faas_sdk.preview._event_outbox_protocol import SQL as OUTBOX
from faas_sdk.preview._stripe_webhook_protocol import SQL, STRIPE_WEBHOOK_SCHEMA
from faas_sdk.preview.stripe_webhook_state import digest, receipt_id

ROOT = Path(__file__).resolve().parents[1] / "request-contracts"
if not ROOT.exists():
    ROOT = Path(__file__).resolve().parents[2] / "request-contracts"
F = json.loads((ROOT / "stripe-webhooks.json").read_text())
SCOPE = PipelineScope.from_dict(F["scope"])
CONTRACT = EventContract(F["contract"])


def delivery(event=None):
    event = F["event"] if event is None else event
    body = (
        F["body"].encode()
        if event is F["event"]
        else json.dumps(event, ensure_ascii=False, separators=(",", ":")).encode()
    )
    headers = F["headers"] | {
        "x-faas-invocation-id": receipt_id(F["endpoint_id"], event["id"]),
        "x-gregale-webhook-event-id": event["id"],
        "x-gregale-webhook-event-type": event["type"],
        "x-gregale-webhook-body-sha256": digest(body),
    }
    return StripeWebhookDelivery(method="POST", path=F["path"], headers=headers, body=body)


class Pool:
    def __init__(self):
        self.ledger, self.outbox = {}, {}
        self.paid = self.active = self.revoked = self.cancelled = False
        self.lose_commit = self.hang_commit = self.invalid = self.fail_apply = False
        self.disabled = self.wrong_app = False
        self.owner, self.applies, self.auth, self.http = "alice", 0, 0, 0

    async def connect(self):
        pool = self

        class Connection:
            async def release(self, discard=False):
                pool.active = False

            async def execute(self, sql, values):
                return await self.query(sql, values)

            async def query(self, sql, values):
                key = tuple(values[:4])
                if sql.startswith("BEGIN"):
                    self.ledger, self.outbox, self.paid = (
                        copy.deepcopy(pool.ledger),
                        copy.deepcopy(pool.outbox),
                        pool.paid,
                    )
                    pool.active = True
                    return []
                if sql.startswith("SELECT set_config") or sql == SQL["lock"]:
                    return []
                if sql == "ROLLBACK":
                    pool.active = False
                    return []
                if sql == "COMMIT":
                    pool.ledger, pool.outbox, pool.paid = self.ledger, self.outbox, self.paid
                    pool.active = False
                    if pool.hang_commit:
                        await asyncio.Event().wait()
                    if pool.lose_commit:
                        raise RuntimeError("private commit diagnostics")
                    return []
                if sql == SQL["reserve"]:
                    if key in self.ledger:
                        return []
                    self.ledger[key] = dict(
                        identity=values[4],
                        binding_digest=values[5],
                        body_digest=values[6],
                        state="processing",
                        owner_id=None,
                        outbox_key=None,
                        reason="",
                        created_at="2026-11-16T10:00:00.123456Z",
                        updated_at="2026-11-16T10:00:00.123456Z",
                    )
                    return [dict(event_id=values[3])]
                if sql == SQL["load"]:
                    return [self.ledger[key]] if key in self.ledger else []
                if sql == SQL["complete"]:
                    row = self.ledger[key] | dict(
                        state=values[4], owner_id=values[5], outbox_key=values[6], reason=values[7]
                    )
                    self.ledger[key] = row
                    return [row]
                if sql == "business.read":
                    return [dict(paid=self.paid, owner=pool.owner, cancelled=pool.cancelled)]
                if sql == "business.write":
                    self.paid = True
                    return []
                outkey = tuple(values[:3])
                if sql == OUTBOX["stage"]:
                    self.outbox[outkey] = dict(request_digest=values[4], app_slug=values[3])
                    return []
                if sql == OUTBOX["stage_check"]:
                    return [self.outbox[outkey]]
                if sql == OUTBOX["status"]:
                    return [
                        dict(
                            producer_key=values[2],
                            app_slug=SCOPE.app_slug,
                            state="pending",
                            attempts="0",
                            next_attempt_at="2026-11-16T10:00:00Z",
                            retry_until="2026-11-17T10:00:00Z",
                            created_at="2026-11-16T10:00:00Z",
                            updated_at="2026-11-16T10:00:00Z",
                            claim_expires_at=None,
                            accepted_at=None,
                            last_code="",
                        )
                    ]
                raise AssertionError("unexpected SQL")

        return Connection()


def fixture(**overrides):
    pool = Pool()

    async def native(request):
        pool.http += 1
        assert not pool.active
        assert request.headers["authorization"] == "Bearer backend"
        path = request.url.path
        data = (
            dict(id=SCOPE.account_id)
            if path == "/v1/account"
            else dict(id=F["endpoint_id"] if pool.wrong_app else SCOPE.app_id)
        )
        if path.endswith("/inbound-webhooks/" + F["endpoint_id"]):
            data = dict(
                id=F["endpoint_id"],
                app_id=SCOPE.app_id,
                account_id=SCOPE.account_id,
                provider="stripe",
                delivery_path=F["path"],
                enabled=not pool.disabled,
            )
        return httpx.Response(200, json=data)

    async def verify(d):
        return stripe_delivery_context_from_headers(d.headers, d.body, SCOPE.account_id, SCOPE.app_id)

    async def authorize(tx, event, actor, action):
        pool.auth += 1
        await tx.execute("business.read", [])
        return pool.owner if actor == "alice" and not pool.revoked else None

    async def apply(tx, event, owner, actor):
        pool.applies += 1
        row = (await tx.execute("business.read", []))[0]
        if row["cancelled"]:
            return StripeWebhookIgnore(reason="cancelled")
        if row["paid"]:
            return StripeWebhookIgnore(reason="already_applied")
        if event.type != "payment_intent.succeeded":
            return StripeWebhookIgnore(reason="superseded")
        await tx.execute("business.write", [])
        if pool.fail_apply:
            raise RuntimeError("private callback payload")
        return StripeWebhookStage(input={} if pool.invalid else dict(import_id=F["import_id"]))

    args = dict(
        name="paid-imports",
        scope=SCOPE,
        endpoint_id=F["endpoint_id"],
        delivery_path=F["path"],
        api_version=F["api_version"],
        livemode=False,
        event_types=["payment_intent.succeeded", "payment_intent.payment_failed"],
        pool=pool,
        token="backend",
        verify=verify,
        authorize=authorize,
        apply=apply,
        httpx_args=dict(transport=httpx.MockTransport(native)),
    )
    args.update(overrides)
    return StripeWebhookAdapter(CONTRACT, **args), pool, args


def test_shared_native_vectors_and_schema():
    assert receipt_id(F["endpoint_id"], F["event"]["id"]) == F["receipt_id"]
    assert hashlib.sha256(STRIPE_WEBHOOK_SCHEMA.encode()).hexdigest() == F["schema_sha256"]
    assert SQL == json.loads((ROOT / "stripe-webhook-sql.json").read_text())
    assert (
        stripe_delivery_context_from_headers(
            F["headers"], F["body"].encode(), SCOPE.account_id, SCOPE.app_id
        ).receipt_id
        == F["receipt_id"]
    )
    with pytest.raises(StripeWebhookError):
        stripe_delivery_context_from_headers(F["headers"], b"tampered", SCOPE.account_id, SCOPE.app_id)


@pytest.mark.asyncio
async def test_atomic_stage_duplicate_receipt_and_shared_binding():
    adapter, pool, _ = fixture()
    first = await adapter.handle(delivery(), "alice", timeout=1)
    assert first.identity == F["identity"] and first.outcome == "staged" and not first.replayed
    assert (await adapter.handle(delivery(), "alice", timeout=1)).replayed
    assert pool.applies == 1 and pool.auth == 2 and len(pool.outbox) == 1 and pool.paid
    receipt = await adapter.inspect(delivery(), "alice", timeout=1)
    assert receipt.created_at == "2026-11-16T10:00:00.123456Z"
    assert next(iter(pool.ledger.values()))["binding_digest"].hex() == F["binding_digest"]
    assert "cus_alice" not in json.dumps(receipt.to_dict())


@pytest.mark.asyncio
async def test_distinct_ids_and_out_of_order_event_do_not_repeat_business_effect():
    adapter, pool, _ = fixture()
    await adapter.handle(delivery(), "alice", timeout=1)
    assert (
        await adapter.handle(delivery(F["event"] | dict(id="evt_another")), "alice", timeout=1)
    ).reason == "already_applied"
    assert (
        await adapter.handle(
            delivery(F["event"] | dict(id="evt_failure", type="payment_intent.payment_failed")), "alice", timeout=1
        )
    ).reason == "already_applied"
    assert len(pool.outbox) == 1


@pytest.mark.asyncio
async def test_authorization_is_current_and_provider_claims_are_not_owner_authority():
    adapter, pool, _ = fixture()
    with pytest.raises(StripeWebhookError, match="unauthorized"):
        await adapter.handle(delivery(), "bob", timeout=1)
    assert not pool.ledger
    await adapter.handle(delivery(), "alice", timeout=1)
    pool.revoked = True
    with pytest.raises(StripeWebhookError, match="unauthorized"):
        await adapter.handle(delivery(), "alice", timeout=1)
    pool.revoked, pool.owner = False, "bob"
    with pytest.raises(StripeWebhookError, match="unauthorized"):
        await adapter.inspect(delivery(), "alice", timeout=1)
    assert pool.applies == 1


@pytest.mark.asyncio
async def test_cancellation_business_and_rollback():
    for mode in ("cancelled", "invalid", "fail_apply"):
        adapter, pool, _ = fixture()
        setattr(pool, mode, True)
        if mode == "cancelled":
            assert (await adapter.handle(delivery(), "alice", timeout=1)).reason == "cancelled"
            assert (await adapter.handle(delivery(), "alice", timeout=1)).replayed
        else:
            with pytest.raises(StripeWebhookError, match="store_failed"):
                await adapter.handle(delivery(), "alice", timeout=1)
            assert not pool.paid and not pool.ledger
        assert not pool.outbox


@pytest.mark.asyncio
async def test_uncertain_commit_inspect_does_not_repeat_callback():
    for hang in (False, True):
        adapter, pool, _ = fixture()
        pool.lose_commit, pool.hang_commit = not hang, hang
        with pytest.raises(StripeWebhookError, match="commit_unknown") as caught:
            await adapter.handle(delivery(), "alice", timeout=0.03 if hang else 1)
        assert caught.value.identity == F["identity"]
        pool.lose_commit = pool.hang_commit = False
        assert (await adapter.inspect(delivery(), "alice", timeout=1)).state == "staged"
        assert (await adapter.handle(delivery(), "alice", timeout=1)).replayed and pool.applies == 1


@pytest.mark.asyncio
async def test_scope_pin_conflicts_and_unsupported_types():
    for patch in (dict(api_version="2026-01-01"), dict(livemode=True), dict(data=dict(object={}))):
        adapter, pool, _ = fixture()
        with pytest.raises(StripeWebhookError):
            await adapter.handle(delivery(F["event"] | patch), "alice", timeout=1)
        assert pool.applies == 0
    adapter, pool, args = fixture()
    assert (
        await adapter.handle(delivery(F["event"] | dict(id="evt_other", type="customer.created")), "alice", timeout=1)
    ).reason == "event_type"
    assert not pool.ledger
    pool.wrong_app = True
    with pytest.raises(StripeWebhookError, match="scope_mismatch"):
        await adapter.handle(delivery(), "alice", timeout=1)
    pool.wrong_app = False
    await adapter.handle(delivery(), "alice", timeout=1)
    with pytest.raises(StripeWebhookError, match="conflict"):
        await adapter.handle(delivery(F["event"] | dict(created=F["event"]["created"] + 1)), "alice", timeout=1)
    with pytest.raises(StripeWebhookError, match="conflict"):
        await StripeWebhookAdapter(CONTRACT, **(args | dict(name="changed"))).handle(delivery(), "alice", timeout=1)
    pool.disabled = True
    with pytest.raises(StripeWebhookError, match="endpoint_unavailable"):
        await adapter.handle(delivery(), "alice", timeout=1)
    assert (await adapter.inspect(delivery(), "alice", timeout=1)).state == "staged"


def test_native_replay_lineage_keeps_precise_generation_and_identity():
    headers = F["headers"] | {
        "x-faas-invocation-source": "replay",
        "x-faas-invocation-id": F["endpoint_id"],
        "x-faas-invocation-replay-root": F["receipt_id"],
        "x-faas-invocation-replay-generation": "9007199254740993",
    }
    assert (
        stripe_delivery_context_from_headers(headers, F["body"].encode(), SCOPE.account_id, SCOPE.app_id).receipt_id
        == F["receipt_id"]
    )
    with pytest.raises(StripeWebhookError):
        stripe_delivery_context_from_headers(
            headers | {"x-faas-invocation-replay-root": F["endpoint_id"]},
            F["body"].encode(),
            SCOPE.account_id,
            SCOPE.app_id,
        )


@pytest.mark.asyncio
async def test_deadline_and_task_cancellation_before_database():
    started = asyncio.Event()

    async def slow_verify(d):
        started.set()
        await asyncio.Event().wait()

    adapter, pool, _ = fixture(verify=slow_verify)
    with pytest.raises(StripeWebhookError, match="deadline"):
        await adapter.handle(delivery(), "alice", timeout=0.01)
    task = asyncio.create_task(adapter.handle(delivery(), "alice", timeout=1))
    await started.wait()
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task
    assert pool.http == 0 and not pool.ledger
