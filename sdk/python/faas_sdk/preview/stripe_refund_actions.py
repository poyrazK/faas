"""Stripe API v1 refunds using a pinned Gregale outbound integration."""

from __future__ import annotations

import re
from collections.abc import Mapping
from typing import Any, Literal, TypedDict
from urllib.parse import urlencode

from ..job_runtime import JobContract, job_value_hash
from .outbound_action_state import (
    ActionState,
    OutboundActionError,
    OutboundActionIntent,
    OutboundActionObservation,
    OutboundActionRecord,
    OutboundActionRequest,
)
from .outbound_actions import OutboundActionReader, OutboundActionResponse


class StripeRefundInput(TypedDict):
    payment_intent: str
    amount_minor: int
    currency: str
    reason: Literal["requested_by_customer", "duplicate"]


class StripeRefundResult(TypedDict):
    refund_id: str
    payment_intent: str
    amount_minor: int
    currency: str
    status: Literal["succeeded", "pending", "requires_action", "failed", "canceled"]


stripe_refund_contract = JobContract[StripeRefundInput, StripeRefundResult](
    dict(
        version=1,
        name="stripe-refund",
        input=dict(
            type="object",
            properties=dict(
                payment_intent=dict(type="string", minLength=4, maxLength=131),
                amount_minor=dict(type="integer", minimum=1, maximum=9007199254740991),
                currency=dict(type="string", minLength=3, maxLength=3),
                reason=dict(type="string", enum=["requested_by_customer", "duplicate"]),
            ),
            required=["payment_intent", "amount_minor", "currency", "reason"],
            additionalProperties=False,
        ),
        output=dict(
            type="object",
            properties=dict(
                refund_id=dict(type="string"),
                payment_intent=dict(type="string"),
                amount_minor=dict(type="integer", minimum=1, maximum=9007199254740991),
                currency=dict(type="string"),
                status=dict(type="string", enum=["succeeded", "pending", "requires_action", "failed", "canceled"]),
            ),
            required=["refund_id", "payment_intent", "amount_minor", "currency", "status"],
            additionalProperties=False,
        ),
    )
)


class StripeRefundActionProvider:
    name = "stripe_refund"

    def __init__(self, *, account_id: str, api_version: str, max_pages: int = 5):
        if (
            re.fullmatch(r"acct_[A-Za-z0-9]{1,128}", account_id) is None
            or re.fullmatch(r"\d{4}-\d{2}-\d{2}\.[a-z]+", api_version) is None
            or type(max_pages) is not int
            or not 1 <= max_pages <= 10
        ):
            raise OutboundActionError("invalid_input")
        self._account, self._version, self._pages = account_id, api_version, max_pages
        self.configuration: Mapping[str, str | int | bool] = dict(
            account_id=account_id, api_version=api_version, max_pages=max_pages
        )

    def request(self, input: StripeRefundInput, identity: str) -> OutboundActionRequest:
        value = stripe_refund_contract.input(input)
        if (
            re.fullmatch(r"pi_[A-Za-z0-9]{1,128}", value["payment_intent"]) is None
            or re.fullmatch(r"[a-z]{3}", value["currency"]) is None
        ):
            raise OutboundActionError("invalid_input")
        body = urlencode(
            [
                ("payment_intent", value["payment_intent"]),
                ("amount", str(value["amount_minor"])),
                ("currency", value["currency"]),
                ("reason", value["reason"]),
                ("metadata[gregale_action_id]", identity),
                ("metadata[gregale_input_sha256]", job_value_hash(value)),
            ]
        )
        return dict(
            path="/v1/refunds", content_type="application/x-www-form-urlencoded", body=body, api_version=self._version
        )

    async def verify(self, reader: OutboundActionReader) -> bool:
        response = await reader.get("/v1/account")
        return response.status == 200 and isinstance(response.body, dict) and response.body.get("id") == self._account

    def _refund(
        self, raw: Any, intent: OutboundActionIntent[StripeRefundInput]
    ) -> OutboundActionObservation[StripeRefundResult]:
        value = intent["input"]
        if (
            not isinstance(raw, dict)
            or raw.get("object") != "refund"
            or not isinstance(raw.get("id"), str)
            or re.fullmatch(r"re_[A-Za-z0-9]{1,128}", raw["id"]) is None
            or raw.get("payment_intent") != value["payment_intent"]
            or type(raw.get("amount")) is not int
            or raw["amount"] != value["amount_minor"]
            or raw.get("currency") != value["currency"]
            or not isinstance(raw.get("metadata"), dict)
            or raw["metadata"].get("gregale_action_id") != intent["identity"]
            or raw["metadata"].get("gregale_input_sha256") != intent["input_sha256"]
            or raw.get("status") not in ("succeeded", "pending", "requires_action", "failed", "canceled")
        ):
            return {"state": "needs_review", "code": "provider_binding_mismatch"}
        state: ActionState = (
            "succeeded"
            if raw["status"] == "succeeded"
            else "pending"
            if raw["status"] == "pending"
            else "needs_review"
            if raw["status"] == "requires_action"
            else "failed"
        )
        return {
            "state": state,
            "code": "refund_" + raw["status"],
            "reference": raw["id"],
            "result": {
                "refund_id": raw["id"],
                "payment_intent": raw["payment_intent"],
                "amount_minor": raw["amount"],
                "currency": raw["currency"],
                "status": raw["status"],
            },
        }

    def decode(
        self, response: OutboundActionResponse, intent: OutboundActionIntent[StripeRefundInput]
    ) -> OutboundActionObservation[StripeRefundResult]:
        if 200 <= response.status < 300:
            return self._refund(response.body, intent)
        if response.status in (400, 401, 402, 403, 404, 422):
            return {"state": "failed", "code": "provider_rejected"}
        return {"state": "unknown", "code": "dispatch_unconfirmed"}

    async def reconcile(
        self, reader: OutboundActionReader, record: OutboundActionRecord[StripeRefundInput, StripeRefundResult]
    ) -> OutboundActionObservation[StripeRefundResult]:
        if record["outcome"].get("reference"):
            response = await reader.get("/v1/refunds/" + record["outcome"]["reference"])
            return (
                self._refund(response.body, record["intent"])
                if response.status == 200
                else {"state": "unknown", "code": "provider_read_unconfirmed"}
            )
        cursor, match, seen = "", None, set()
        for _page in range(self._pages):
            query = (
                "?payment_intent="
                + record["intent"]["input"]["payment_intent"]
                + "&limit=100"
                + ("&starting_after=" + cursor if cursor else "")
            )
            response = await reader.get("/v1/refunds" + query)
            raw = response.body
            if response.status != 200:
                return {"state": "unknown", "code": "provider_read_unconfirmed"}
            if (
                not isinstance(raw, dict)
                or raw.get("object") != "list"
                or not isinstance(raw.get("data"), list)
                or len(raw["data"]) > 100
                or type(raw.get("has_more")) is not bool
            ):
                return {"state": "needs_review", "code": "provider_response_invalid"}
            for item in raw["data"]:
                if (
                    not isinstance(item, dict)
                    or not isinstance(item.get("id"), str)
                    or re.fullmatch(r"re_[A-Za-z0-9]{1,128}", item["id"]) is None
                    or item["id"] in seen
                ):
                    return {"state": "needs_review", "code": "provider_response_invalid"}
                seen.add(item["id"])
                if (
                    isinstance(item.get("metadata"), dict)
                    and item["metadata"].get("gregale_action_id") == record["intent"]["identity"]
                ):
                    if match is not None:
                        return {"state": "needs_review", "code": "multiple_provider_matches"}
                    match = item
            if not raw["has_more"]:
                return (
                    self._refund(match, record["intent"])
                    if match is not None
                    else {"state": "unknown", "code": "provider_not_observed"}
                )
            if not raw["data"]:
                return {"state": "needs_review", "code": "provider_response_invalid"}
            cursor = raw["data"][-1]["id"]
        return {"state": "unknown", "code": "reconciliation_limit"}
