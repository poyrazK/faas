from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EventRecoveryRateRequest")


@_attrs_define
class EventRecoveryRateRequest:
    """Replacement admission rate and optional audit reason for an existing recovery job."""

    rate_per_second: int
    """Maximum items per second. Does not reset spent permits or existing admission waits."""
    reason: str | Unset = UNSET
    """Reason in the event recovery rate request: optional operator reason, limited to 512 UTF-8 bytes without
    control characters. Stored only in audit history; omitted from frozen selection. Preview does not record it."""

    def to_dict(self) -> dict[str, Any]:
        rate_per_second = self.rate_per_second

        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "rate_per_second": rate_per_second,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        rate_per_second = d.pop("rate_per_second")

        reason = d.pop("reason", UNSET)

        event_recovery_rate_request = cls(
            rate_per_second=rate_per_second,
            reason=reason,
        )

        return event_recovery_rate_request
