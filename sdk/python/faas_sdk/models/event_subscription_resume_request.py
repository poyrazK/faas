from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EventSubscriptionResumeRequest")


@_attrs_define
class EventSubscriptionResumeRequest:
    rate_per_second: int | Unset = 10
    """Maximum new routing admissions in a one-second window; zero removes pacing. This limit continues to apply to
    new publications after the backlog drains."""

    def to_dict(self) -> dict[str, Any]:
        rate_per_second = self.rate_per_second

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if rate_per_second is not UNSET:
            field_dict["rate_per_second"] = rate_per_second

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        rate_per_second = d.pop("rate_per_second", UNSET)

        event_subscription_resume_request = cls(
            rate_per_second=rate_per_second,
        )

        return event_subscription_resume_request
