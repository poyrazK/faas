from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.publish_event_batch_result_status import (
    PublishEventBatchResultStatus,
    check_publish_event_batch_result_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.problem import Problem
    from ..models.publish_event_response import PublishEventResponse


T = TypeVar("T", bound="PublishEventBatchResult")


@_attrs_define
class PublishEventBatchResult:
    """Durable acceptance or failure observation for one batch input position."""

    index: int
    """Zero-based input position."""
    status: PublishEventBatchResultStatus
    retryable: bool
    """Retry using exactly the original event identity and content."""
    receipt: PublishEventResponse | Unset = UNSET
    """Durable acceptance receipt for a published internal event."""
    problem: Problem | Unset = UNSET
    """RFC 9457 problem+json envelope. The `code` field is the stable
    machine-readable identifier; clients branch on it. `limit` and
    `observed` are populated on quota errors. `docs_url` points the
    user at the next action. Common customer-facing codes and recovery
    guidance are listed in `docs/errors.md`. `billing_portal_url` is
    populated on `code: payment_required` when the customer already has a
    provider subscription and must update it in the provider
    portal. `checkout_url` is populated when a new hosted checkout
    is required. `paddle_checkout_url` is retained as a legacy
    alias for Paddle clients, and `tx_id` carries the provider
    checkout handle when one exists.

    `errors` carries per-field detail (Cloudflare / Stripe shape)
    for 422 sites that emit a list of field-level failures — used
    today by the kind=validate edge rule so a JSON Schema
    rejection renders as a form-field list the dashboard can
    iterate without parsing prose. Optional + omitempty so every
    other problem+json site keeps its existing flat shape unchanged.
    """

    def to_dict(self) -> dict[str, Any]:
        index = self.index

        status: str = self.status

        retryable = self.retryable

        receipt: dict[str, Any] | Unset = UNSET
        if not isinstance(self.receipt, Unset):
            receipt = self.receipt.to_dict()

        problem: dict[str, Any] | Unset = UNSET
        if not isinstance(self.problem, Unset):
            problem = self.problem.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "index": index,
                "status": status,
                "retryable": retryable,
            }
        )
        if receipt is not UNSET:
            field_dict["receipt"] = receipt
        if problem is not UNSET:
            field_dict["problem"] = problem

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.problem import Problem
        from ..models.publish_event_response import PublishEventResponse

        d = dict(src_dict)
        index = d.pop("index")

        status = check_publish_event_batch_result_status(d.pop("status"))

        retryable = d.pop("retryable")

        _receipt = d.pop("receipt", UNSET)
        receipt: PublishEventResponse | Unset
        if isinstance(_receipt, Unset):
            receipt = UNSET
        else:
            receipt = PublishEventResponse.from_dict(_receipt)

        _problem = d.pop("problem", UNSET)
        problem: Problem | Unset
        if isinstance(_problem, Unset):
            problem = UNSET
        else:
            problem = Problem.from_dict(_problem)

        publish_event_batch_result = cls(
            index=index,
            status=status,
            retryable=retryable,
            receipt=receipt,
            problem=problem,
        )

        return publish_event_batch_result
