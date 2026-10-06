from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_receipt_recovery_action_kind import (
    EventReceiptRecoveryActionKind,
    check_event_receipt_recovery_action_kind,
)
from ..models.event_receipt_recovery_action_method import (
    EventReceiptRecoveryActionMethod,
    check_event_receipt_recovery_action_method,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.replay_event_fanout_failure_request import ReplayEventFanoutFailureRequest


T = TypeVar("T", bound="EventReceiptRecoveryAction")


@_attrs_define
class EventReceiptRecoveryAction:
    """Selective recovery request through an existing authorized endpoint. GET receipt inspection never performs this
    action.

    """

    kind: EventReceiptRecoveryActionKind
    method: EventReceiptRecoveryActionMethod
    url: str
    body: ReplayEventFanoutFailureRequest | Unset = UNSET
    """Identity of one terminal fanout failure to replay."""

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        method: str = self.method

        url = self.url

        body: dict[str, Any] | Unset = UNSET
        if not isinstance(self.body, Unset):
            body = self.body.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
                "method": method,
                "url": url,
            }
        )
        if body is not UNSET:
            field_dict["body"] = body

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.replay_event_fanout_failure_request import ReplayEventFanoutFailureRequest

        d = dict(src_dict)
        kind = check_event_receipt_recovery_action_kind(d.pop("kind"))

        method = check_event_receipt_recovery_action_method(d.pop("method"))

        url = d.pop("url")

        _body = d.pop("body", UNSET)
        body: ReplayEventFanoutFailureRequest | Unset
        if isinstance(_body, Unset):
            body = UNSET
        else:
            body = ReplayEventFanoutFailureRequest.from_dict(_body)

        event_receipt_recovery_action = cls(
            kind=kind,
            method=method,
            url=url,
            body=body,
        )

        return event_receipt_recovery_action
