from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.queue_send_batch_result_status import QueueSendBatchResultStatus, check_queue_send_batch_result_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.problem import Problem
    from ..models.queue_send_response import QueueSendResponse


T = TypeVar("T", bound="QueueSendBatchResult")


@_attrs_define
class QueueSendBatchResult:
    """The outcome of one message by its zero-based input index: accepted, rejected with the problem, or not attempted
    after an earlier rejection.

    """

    index: int
    """Zero-based input position."""
    status: QueueSendBatchResultStatus
    message: QueueSendResponse | Unset = UNSET
    """201 — body of a freshly-enqueued queue row."""
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
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        index = self.index

        status: str = self.status

        message: dict[str, Any] | Unset = UNSET
        if not isinstance(self.message, Unset):
            message = self.message.to_dict()

        problem: dict[str, Any] | Unset = UNSET
        if not isinstance(self.problem, Unset):
            problem = self.problem.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "index": index,
                "status": status,
            }
        )
        if message is not UNSET:
            field_dict["message"] = message
        if problem is not UNSET:
            field_dict["problem"] = problem

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.problem import Problem
        from ..models.queue_send_response import QueueSendResponse

        d = dict(src_dict)
        index = d.pop("index")

        status = check_queue_send_batch_result_status(d.pop("status"))

        _message = d.pop("message", UNSET)
        message: QueueSendResponse | Unset
        if isinstance(_message, Unset):
            message = UNSET
        else:
            message = QueueSendResponse.from_dict(_message)

        _problem = d.pop("problem", UNSET)
        problem: Problem | Unset
        if isinstance(_problem, Unset):
            problem = UNSET
        else:
            problem = Problem.from_dict(_problem)

        queue_send_batch_result = cls(
            index=index,
            status=status,
            message=message,
            problem=problem,
        )

        queue_send_batch_result.additional_properties = d
        return queue_send_batch_result

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
