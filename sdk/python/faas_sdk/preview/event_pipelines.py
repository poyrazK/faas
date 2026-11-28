"""Authenticated CloudEvents -> typed native workflows with durable start receipts."""

from __future__ import annotations

import asyncio
import copy
import json
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass
from typing import Any, Generic, Literal, TypeVar

from ..job_runtime import JobContract, JobError, _scope, job_hash, job_value_hash
from ._event_consumer_contract import EVENT_CONSUMER_REQUEST_MAX_BYTES
from .event_consumer_transactions import EventConsumerRequest, new_event_consumer_request
from .event_consumer_transactions import _uuid as consumer_uuid
from .event_contract import EventContract
from .event_pipeline_state import (
    EventPipelineError,
    EventPipelineReceipt,
    EventPipelineStore,
    event_pipeline_identity,
    event_pipeline_scope,
    match_event_pipeline_checkpoint,
)
from .job_pipeline_client import JobPipelineClient
from .pipeline_state import PipelineError, PipelineScope, pipeline_name

E = TypeVar("E")
I = TypeVar("I")
O = TypeVar("O")
A = TypeVar("A")
T = TypeVar("T")
EventPipelineAction = Literal["start", "inspect", "reconcile"]


@dataclass(frozen=True, kw_only=True)
class EventPipelineDelivery:
    headers: Mapping[str, str | Sequence[str]]
    body: bytes


@dataclass(frozen=True, kw_only=True)
class PipelineEvent(Generic[E]):
    identity: EventConsumerRequest
    type: str
    data: E


@dataclass(frozen=True, kw_only=True)
class EventPipelineOutcome:
    state: Literal["started", "start_unknown"]
    receipt: EventPipelineReceipt
    replayed: bool


def _capture(delivery: EventPipelineDelivery) -> EventPipelineDelivery:
    try:
        if not isinstance(delivery.body, bytes) or not 0 < len(delivery.body) <= EVENT_CONSUMER_REQUEST_MAX_BYTES:
            raise ValueError
        return EventPipelineDelivery(headers=copy.deepcopy(dict(delivery.headers)), body=bytes(delivery.body))
    except Exception:
        raise EventPipelineError("invalid_event") from None


def _outcome(receipt: EventPipelineReceipt, replayed: bool) -> EventPipelineOutcome:
    return EventPipelineOutcome(
        state="started" if receipt.checkpoint is not None else "start_unknown", receipt=receipt, replayed=replayed
    )


class EventPipeline(Generic[E, I, O, A]):
    """Authenticate deliveries and check current owner authority on every operation.

    Only an acknowledged new reservation can start. Replays never dispatch again,
    including after native idempotency retention expires. Unknown starts require
    explicit verified-ID reconciliation. User mapping must be side-effect free.
    """

    def __init__(
        self,
        event_contract: EventContract[E],
        *,
        name: str,
        scope: PipelineScope,
        subscription_id: str,
        source: str,
        contract: JobContract[I, O],
        store: EventPipelineStore,
        token: str,
        verify: Callable[[EventPipelineDelivery], Awaitable[EventConsumerRequest]],
        authorize: Callable[[PipelineEvent[E], A, EventPipelineAction], Awaitable[str | None]],
        map: Callable[[PipelineEvent[E], str], Awaitable[I]],
        httpx_args: dict[str, Any] | None = None,
    ):
        self._scope = event_pipeline_scope(scope)
        self._subscription_id = consumer_uuid(subscription_id)
        if not pipeline_name(name) or not source or not isinstance(token, str) or not token:
            raise EventPipelineError("invalid_event")
        self._event_contract, self._contract, self._store = event_contract, contract, store
        self._name, self._source, self._token = name, source, token
        self._verify, self._authorize, self._map = verify, authorize, map
        self._httpx_args = dict(httpx_args or {})

    async def _operation(
        self, timeout: float, work: Callable[[Callable[[], None], Callable[[], float]], Awaitable[T]]
    ) -> T:
        async def run(check: Callable[[], None]) -> T:
            deadline = asyncio.get_running_loop().time() + timeout

            def remaining() -> float:
                check()
                return max(0.000001, deadline - asyncio.get_running_loop().time())

            value = await work(check, remaining)
            check()
            return value

        try:
            return await _scope(timeout, run)
        except JobError as error:
            raise EventPipelineError("deadline" if error.code == "deadline" else "invalid_event") from None

    async def _stored(self, check: Callable[[], None], work: Callable[[], Awaitable[T]]) -> T:
        check()
        try:
            value = await work()
            check()
            return value
        except EventPipelineError:
            raise
        except Exception:
            check()
            raise EventPipelineError("store_failed") from None

    async def _prepare(
        self, delivery: EventPipelineDelivery, access: A, action: EventPipelineAction, check: Callable[[], None]
    ) -> tuple[PipelineEvent[E], str, dict[str, Any]]:
        captured = _capture(delivery)
        try:
            identity = await self._verify(_capture(captured))
            check()
        except Exception:
            check()
            raise EventPipelineError("unauthorized") from None
        try:
            normalized = new_event_consumer_request(
                identity.account_id,
                identity.app_id,
                identity.subscription_id,
                identity.acceptance_id,
                identity.source,
                identity.event_id,
                captured.body,
            )
            body = json.loads(captured.body)
            if (
                normalized.account_id != self._scope.account_id
                or normalized.app_id != self._scope.app_id
                or normalized.subscription_id != self._subscription_id
                or normalized.source != self._source
            ):
                raise EventPipelineError("scope_mismatch")
            if body["type"] != self._event_contract.spec["type"]:
                raise EventPipelineError("invalid_event")
            event = PipelineEvent(identity=normalized, type=body["type"], data=self._event_contract.input(body["data"]))
        except EventPipelineError:
            raise
        except Exception:
            raise EventPipelineError("invalid_event") from None
        try:
            owner = await self._authorize(copy.deepcopy(event), access, action)
            check()
        except Exception:
            check()
            raise EventPipelineError("unauthorized") from None
        if not pipeline_name(owner):
            raise EventPipelineError("unauthorized")
        assert isinstance(owner, str)
        identity_hash = event_pipeline_identity(
            self._scope, self._name, self._subscription_id, str(normalized.acceptance_id)
        )
        binding = {
            "version": 1,
            "identity": identity_hash,
            "scope": self._scope.to_dict(),
            "name": self._name,
            "subscription_id": self._subscription_id,
            "acceptance_id": str(normalized.acceptance_id),
            "source": normalized.source,
            "event_id": normalized.event_id,
            "event_type": event.type,
            "body_sha256": job_hash(captured.body),
            "event_contract_sha256": self._event_contract.sha256,
            "pipeline_sha256": self._contract.sha256,
            "owner_id": owner,
            "idempotency_key": "event-pipeline:" + identity_hash,
        }
        return event, owner, binding

    @staticmethod
    def _bound(binding: dict[str, Any], raw: EventPipelineReceipt) -> EventPipelineReceipt:
        try:
            receipt = EventPipelineReceipt.from_dict(raw.to_dict())
            intent = receipt.intent
            if any(job_value_hash(value) != job_value_hash(intent[key]) for key, value in binding.items()):
                raise ValueError
            return receipt
        except Exception:
            raise EventPipelineError("receipt_conflict") from None

    def _client(self, owner: str) -> JobPipelineClient[I, O]:
        return JobPipelineClient(
            base_url=self._scope.base_url,
            scope=self._scope,
            owner_id=owner,
            token=self._token,
            contract=self._contract,
            httpx_args=self._httpx_args,
        )

    async def handle(self, delivery: EventPipelineDelivery, access: A, *, timeout: float) -> EventPipelineOutcome:
        captured = _capture(delivery)

        async def work(check: Callable[[], None], remaining: Callable[[], float]) -> EventPipelineOutcome:
            event, owner, binding = await self._prepare(captured, access, "start", check)
            try:
                value = self._contract.input(await self._map(copy.deepcopy(event), owner))
                check()
            except Exception:
                check()
                raise EventPipelineError("invalid_event") from None
            trigger = {"version": 1, "kind": "event", "identity": binding["identity"]}
            intent = binding | {
                "input_sha256": job_value_hash(
                    {
                        "version": 1,
                        "scope": self._scope.to_dict(),
                        "owner_id": owner,
                        "contract_sha256": self._contract.sha256,
                        "input": value,
                        "trigger": trigger,
                    }
                )
            }
            created, raw = await self._stored(check, lambda: self._store.reserve(copy.deepcopy(intent)))
            receipt = self._bound(binding, raw)
            if type(created) is not bool or job_value_hash(receipt.intent) != job_value_hash(intent):
                raise EventPipelineError("receipt_conflict")
            if not created:
                return _outcome(receipt, True)
            if receipt.checkpoint is not None:
                raise EventPipelineError("receipt_conflict")
            check()
            try:
                async with self._client(owner) as client:
                    run = await client.start(
                        value, timeout=remaining(), idempotency_key=intent["idempotency_key"], trigger=trigger
                    )
                    check()
                    checkpoint = run.checkpoint
            except PipelineError as error:
                check()
                if error.code == "scope_mismatch":
                    raise EventPipelineError("scope_mismatch") from None
                return _outcome(receipt, False)
            except Exception:
                check()
                return _outcome(receipt, False)
            match_event_pipeline_checkpoint(intent, checkpoint)
            completed = self._bound(
                binding, await self._stored(check, lambda: self._store.complete(copy.deepcopy(intent), checkpoint))
            )
            if completed.checkpoint != checkpoint:
                raise EventPipelineError("receipt_conflict")
            return _outcome(completed, False)

        return await self._operation(timeout, work)

    async def inspect(
        self, delivery: EventPipelineDelivery, access: A, *, timeout: float
    ) -> EventPipelineOutcome | None:
        captured = _capture(delivery)

        async def work(check: Callable[[], None], remaining: Callable[[], float]) -> EventPipelineOutcome | None:
            _, _, binding = await self._prepare(captured, access, "inspect", check)
            receipt = await self._stored(check, lambda: self._store.load(binding["identity"]))
            return None if receipt is None else _outcome(self._bound(binding, receipt), True)

        return await self._operation(timeout, work)

    async def reconcile(
        self, delivery: EventPipelineDelivery, run_id: str, access: A, *, timeout: float
    ) -> EventPipelineOutcome:
        """Read and verify the exact native run; never start or restart work."""
        captured = _capture(delivery)

        async def work(check: Callable[[], None], remaining: Callable[[], float]) -> EventPipelineOutcome:
            _, owner, binding = await self._prepare(captured, access, "reconcile", check)
            raw = await self._stored(check, lambda: self._store.load(binding["identity"]))
            if raw is None:
                raise EventPipelineError("receipt_conflict")
            receipt = self._bound(binding, raw)
            if receipt.checkpoint is not None:
                if receipt.checkpoint.run_id != run_id:
                    raise EventPipelineError("receipt_conflict")
                return _outcome(receipt, True)
            try:
                async with self._client(owner) as client:
                    checkpoint = (await client.resume_by_id(run_id, timeout=remaining())).checkpoint
                    check()
            except Exception:
                check()
                raise EventPipelineError("receipt_conflict") from None
            match_event_pipeline_checkpoint(receipt.intent, checkpoint)
            completed = self._bound(
                binding, await self._stored(check, lambda: self._store.complete(receipt.intent, checkpoint))
            )
            if completed.checkpoint != checkpoint:
                raise EventPipelineError("receipt_conflict")
            return _outcome(completed, True)

        return await self._operation(timeout, work)
