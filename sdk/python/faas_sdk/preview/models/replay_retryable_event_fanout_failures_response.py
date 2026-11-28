from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ReplayRetryableEventFanoutFailuresResponse")


@_attrs_define
class ReplayRetryableEventFanoutFailuresResponse:
    """Summary of one bounded app-scoped replay request."""

    app_slug: str
    replayed_count: int
    has_more: bool
    """True when more retryable failures remain for a later request."""

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        replayed_count = self.replayed_count

        has_more = self.has_more

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_slug": app_slug,
                "replayed_count": replayed_count,
                "has_more": has_more,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        replayed_count = d.pop("replayed_count")

        has_more = d.pop("has_more")

        replay_retryable_event_fanout_failures_response = cls(
            app_slug=app_slug,
            replayed_count=replayed_count,
            has_more=has_more,
        )

        return replay_retryable_event_fanout_failures_response
