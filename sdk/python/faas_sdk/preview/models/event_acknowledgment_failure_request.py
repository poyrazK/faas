from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="EventAcknowledgmentFailureRequest")


@_attrs_define
class EventAcknowledgmentFailureRequest:
    """App-authorized negative acknowledgment for the current live attempt; repeat exactly after an uncertain response."""

    token: str
    """Secret attempt token; never log it or put it in a URL."""
    reason: str
    """Non-sensitive failure code; no payloads or credentials."""

    def to_dict(self) -> dict[str, Any]:
        token = self.token

        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "token": token,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        token = d.pop("token")

        reason = d.pop("reason")

        event_acknowledgment_failure_request = cls(
            token=token,
            reason=reason,
        )

        return event_acknowledgment_failure_request
