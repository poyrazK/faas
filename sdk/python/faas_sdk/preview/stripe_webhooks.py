"""Native verified Stripe deliveries to atomic business writes and typed event intent."""

from __future__ import annotations

import asyncio
import copy
import json
import re
from collections.abc import Awaitable, Callable
from typing import Any, Generic, Literal, TypeVar, cast

import httpx

from ..job_runtime import JobError, _scope, job_value_hash
from ._stripe_webhook_protocol import SQL
from .event_consumer_transactions import _uuid
from .event_contract import EventContract
from .event_outbox import EventPublisher
from .event_outbox_state import EventOutboxError, EventPublicationOptions, _milliseconds
from .event_outbox_transaction import EventOutboxPool, EventOutboxTransaction, with_event_outbox_transaction
from .event_pipeline_state import event_pipeline_scope
from .pipeline_state import PipelineScope, pipeline_name
from .stripe_webhook_state import (
    StripeDeliveryContext,
    StripeWebhookDecision,
    StripeWebhookDelivery,
    StripeWebhookError,
    StripeWebhookEvent,
    StripeWebhookOutcome,
    StripeWebhookReceipt,
    capture,
    digest,
    event_type,
    metadata,
    path_valid,
    receipt_id,
    snapshot,
)
from .stripe_webhook_state import api_version as version_valid
from .stripe_webhook_state import identity as webhook_identity

E = TypeVar("E")
A = TypeVar("A")
T = TypeVar("T")
StripeWebhookAction = Literal["handle", "inspect"]


class StripeWebhookAdapter(Generic[E, A]):
    def __init__(
        self,
        contract: EventContract[E],
        *,
        name: str,
        scope: PipelineScope,
        endpoint_id: str,
        delivery_path: str,
        api_version: str,
        livemode: bool,
        event_types: list[str] | tuple[str, ...],
        pool: EventOutboxPool,
        token: str,
        verify: Callable[[StripeWebhookDelivery], Awaitable[StripeDeliveryContext]],
        authorize: Callable[
            [EventOutboxTransaction, StripeWebhookEvent, A, StripeWebhookAction], Awaitable[str | None]
        ],
        apply: Callable[[EventOutboxTransaction, StripeWebhookEvent, str, A], Awaitable[StripeWebhookDecision[E]]],
        connected_account_id: str | None = None,
        retry_window: float = 86400,
        httpx_args: dict[str, Any] | None = None,
    ):
        self._scope = event_pipeline_scope(scope)
        self._endpoint = _uuid(endpoint_id)
        types = tuple(event_types)
        window = _milliseconds(retry_window, 1000, 86400000)
        args = dict(httpx_args or {})
        if (
            not pipeline_name(name)
            or not path_valid(delivery_path)
            or not version_valid(api_version)
            or type(livemode) is not bool
            or connected_account_id is not None
            and re.fullmatch(r"acct_[A-Za-z0-9]{1,250}", connected_account_id) is None
            or not 1 <= len(types) <= 64
            or len(set(types)) != len(types)
            or any(not event_type(t) for t in types)
            or not token
            or set(args) & {"base_url", "headers", "auth", "params", "cookies", "follow_redirects", "timeout"}
        ):
            raise StripeWebhookError("invalid_delivery")
        self._path, self._version, self._live, self._account = (
            delivery_path,
            api_version,
            livemode,
            connected_account_id,
        )
        self._types, self._pool, self._token, self._verify, self._authorize, self._apply = (
            types,
            pool,
            token,
            verify,
            authorize,
            apply,
        )
        self._window = window
        self._httpx_args = args
        captured = EventContract[E](contract.spec)
        self._binding = bytes.fromhex(
            job_value_hash(
                [
                    "gregale.stripe-webhook-binding.v1",
                    self._scope.to_dict(),
                    self._endpoint,
                    name,
                    delivery_path,
                    api_version,
                    livemode,
                    connected_account_id,
                    sorted(types),
                    captured.sha256,
                    window,
                ]
            )
        )
        self._publisher = EventPublisher(captured, scope=self._scope)

    async def _run(
        self, delivery: StripeWebhookDelivery, access: A, timeout: float, action: StripeWebhookAction
    ) -> Any:
        saved = capture(delivery)
        id = None
        pending = False

        async def run(check: Callable[[], None]) -> Any:
            nonlocal id, pending
            end = asyncio.get_running_loop().time() + timeout

            def remaining() -> float:
                check()
                left = end - asyncio.get_running_loop().time()
                if left <= 0:
                    raise StripeWebhookError("deadline", id)
                return left

            remaining()
            if saved.path != self._path:
                raise StripeWebhookError("scope_mismatch")
            try:
                proof = copy.deepcopy(await self._verify(capture(saved)))
                remaining()
            except Exception:
                remaining()
                raise StripeWebhookError("unauthorized") from None
            if (
                proof.account_id != self._scope.account_id
                or proof.app_id != self._scope.app_id
                or proof.endpoint_id != self._endpoint
                or proof.body_sha256 != digest(saved.body)
                or proof.receipt_id != receipt_id(self._endpoint, proof.event_id)
            ):
                raise StripeWebhookError("scope_mismatch")
            event = snapshot(saved.body, proof, self._version, self._live, self._account)
            id = webhook_identity(self._scope, self._endpoint, event.id)
            current_id = id
            args = [self._scope.account_id, self._scope.app_id, self._endpoint, event.id]
            async with httpx.AsyncClient(
                base_url=self._scope.base_url,
                headers={"authorization": "Bearer " + self._token, "accept": "application/json"},
                follow_redirects=False,
                timeout=None,
                **self._httpx_args,
            ) as client:

                async def read(path: str) -> dict[str, Any]:
                    remaining()
                    async with client.stream("GET", path) as response:
                        if not response.is_success:
                            raise StripeWebhookError("scope_mismatch", current_id)
                        data = bytearray()
                        async for chunk in response.aiter_bytes(chunk_size=8192):
                            remaining()
                            if len(data) + len(chunk) > 1048576:
                                raise StripeWebhookError("scope_mismatch", current_id)
                            data.extend(chunk)
                    remaining()
                    value = json.loads(data)
                    if not isinstance(value, dict):
                        raise StripeWebhookError("scope_mismatch", current_id)
                    return value

                from urllib.parse import quote

                root = "/v1/apps/" + quote(self._scope.app_slug, safe="")
                if (await read("/v1/account")).get("id") != self._scope.account_id or (await read(root)).get(
                    "id"
                ) != self._scope.app_id:
                    raise StripeWebhookError("scope_mismatch", current_id)
                if action == "handle":
                    meta = await read(root + "/inbound-webhooks/" + self._endpoint)
                    if (
                        meta.get("id") != self._endpoint
                        or meta.get("account_id") != self._scope.account_id
                        or meta.get("app_id") != self._scope.app_id
                        or meta.get("provider") != "stripe"
                        or meta.get("delivery_path") != self._path
                    ):
                        raise StripeWebhookError("scope_mismatch", current_id)
                    if meta.get("enabled") is not True:
                        raise StripeWebhookError("endpoint_unavailable", current_id)
                if event.type not in self._types:
                    return (
                        None
                        if action == "inspect"
                        else StripeWebhookOutcome(
                            outcome="ignored", replayed=False, identity=current_id, receipt=None, reason="event_type"
                        )
                    )

                async def transaction(tx: EventOutboxTransaction) -> Any:
                    await tx._query(SQL["lock"], ["gregale.stripe-webhook.v1:" + current_id])
                    remaining()
                    created = False
                    if action == "handle":
                        created = (
                            len(
                                await tx._query(
                                    SQL["reserve"], [*args, current_id, self._binding, bytes.fromhex(proof.body_sha256)]
                                )
                            )
                            == 1
                        )
                    rows = await tx._query(SQL["load"], args)
                    if len(rows) > 1:
                        raise EventOutboxError("store_failed")
                    old = rows[0] if rows else None
                    if old is not None and (
                        bytes(old["binding_digest"]) != self._binding
                        or bytes(old["body_digest"]) != bytes.fromhex(proof.body_sha256)
                        or old["identity"] != current_id
                    ):
                        raise EventOutboxError("conflict")
                    try:
                        owner = await self._authorize(tx, copy.deepcopy(event), access, action)
                        remaining()
                    except Exception:
                        remaining()
                        raise EventOutboxError("scope_mismatch") from None
                    if (
                        not pipeline_name(owner)
                        or old is not None
                        and old.get("owner_id") is not None
                        and old["owner_id"] != owner
                    ):
                        raise EventOutboxError("scope_mismatch")
                    owner = cast(str, owner)
                    if action == "inspect":
                        return metadata(old, current_id, self._endpoint, event.id) if old is not None else None
                    if not created:
                        if old is None:
                            raise EventOutboxError("store_failed")
                        receipt = metadata(old, current_id, self._endpoint, event.id)
                        return StripeWebhookOutcome(
                            outcome=receipt.state,
                            replayed=True,
                            identity=current_id,
                            receipt=receipt,
                            reason=receipt.reason or None,
                        )
                    decision = await self._apply(tx, copy.deepcopy(event), owner, access)
                    remaining()
                    key = None
                    reason = ""
                    if decision.state == "stage":
                        key = "stripe-webhook:" + current_id
                        await self._publisher.stage(
                            tx,
                            decision.input,
                            EventPublicationOptions(key=key),
                            retry_window=self._window / 1000,
                        )
                        remaining()
                    elif decision.state == "ignore" and decision.reason in (
                        "not_applicable",
                        "already_applied",
                        "cancelled",
                        "superseded",
                    ):
                        reason = decision.reason
                    else:
                        raise EventOutboxError("invalid_request")
                    state = "staged" if key else "ignored"
                    completed = await tx._query(SQL["complete"], [*args, state, owner, key, reason])
                    if len(completed) != 1:
                        raise EventOutboxError("store_failed")
                    receipt = metadata(completed[0], current_id, self._endpoint, event.id)
                    return StripeWebhookOutcome(
                        outcome=receipt.state,
                        replayed=False,
                        identity=current_id,
                        receipt=receipt,
                        reason=reason or None,
                    )

                pending = True
                try:
                    return await with_event_outbox_transaction(self._pool, transaction, timeout=remaining())
                finally:
                    pending = False

        try:
            return await _scope(timeout, run)
        except BaseException as error:
            if pending:
                raise StripeWebhookError("commit_unknown", id) from None
            if isinstance(error, StripeWebhookError):
                raise
            if isinstance(error, EventOutboxError):
                code = {
                    "scope_mismatch": "unauthorized",
                    "commit_unknown": "commit_unknown",
                    "conflict": "conflict",
                    "deadline": "deadline",
                    "cancelled": "cancelled",
                }.get(error.code, "store_failed")
                raise StripeWebhookError(code, id) from None
            if isinstance(error, JobError) and error.code == "deadline":
                raise StripeWebhookError("deadline", id) from None
            if not isinstance(error, Exception):
                raise
            raise StripeWebhookError("store_failed", id) from None

    async def handle(self, delivery: StripeWebhookDelivery, access: A, *, timeout: float) -> StripeWebhookOutcome:
        return cast(StripeWebhookOutcome, await self._run(delivery, access, timeout, "handle"))

    async def inspect(
        self, delivery: StripeWebhookDelivery, access: A, *, timeout: float
    ) -> StripeWebhookReceipt | None:
        return cast(StripeWebhookReceipt | None, await self._run(delivery, access, timeout, "inspect"))
