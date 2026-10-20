from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.queue_state_response_plan import QueueStateResponsePlan, check_queue_state_response_plan
from ..types import UNSET, Unset

T = TypeVar("T", bound="QueueStateResponse")


@_attrs_define
class QueueStateResponse:
    """200 — queue depth / in-flight / oldest-pending stats. Read-only."""

    app_slug: str
    plan: QueueStateResponsePlan
    plan_cap: int
    """MaxQueueDepth for the plan."""
    depth: int
    """Pending + dispatching row count."""
    in_flight: int
    """Rows with a live dispatch lease (state=dispatching, lease_expires_at > now or NULL)."""
    generated_at: datetime.datetime
    oldest_pending_at: datetime.datetime | None | Unset = UNSET
    """Oldest pending row's created_at; null when the queue is empty."""
    oldest_pending_age_seconds: int | None | Unset = UNSET
    """Convenience field — seconds since oldest_pending_at; null when the queue is empty."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        plan: str = self.plan

        plan_cap = self.plan_cap

        depth = self.depth

        in_flight = self.in_flight

        generated_at = self.generated_at.isoformat()

        oldest_pending_at: None | str | Unset
        if isinstance(self.oldest_pending_at, Unset):
            oldest_pending_at = UNSET
        elif isinstance(self.oldest_pending_at, datetime.datetime):
            oldest_pending_at = self.oldest_pending_at.isoformat()
        else:
            oldest_pending_at = self.oldest_pending_at

        oldest_pending_age_seconds: int | None | Unset
        if isinstance(self.oldest_pending_age_seconds, Unset):
            oldest_pending_age_seconds = UNSET
        else:
            oldest_pending_age_seconds = self.oldest_pending_age_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_slug": app_slug,
                "plan": plan,
                "plan_cap": plan_cap,
                "depth": depth,
                "in_flight": in_flight,
                "generated_at": generated_at,
            }
        )
        if oldest_pending_at is not UNSET:
            field_dict["oldest_pending_at"] = oldest_pending_at
        if oldest_pending_age_seconds is not UNSET:
            field_dict["oldest_pending_age_seconds"] = oldest_pending_age_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        plan = check_queue_state_response_plan(d.pop("plan"))

        plan_cap = d.pop("plan_cap")

        depth = d.pop("depth")

        in_flight = d.pop("in_flight")

        generated_at = datetime.datetime.fromisoformat(d.pop("generated_at"))

        def _parse_oldest_pending_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                oldest_pending_at_type_0 = datetime.datetime.fromisoformat(data)

                return oldest_pending_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        oldest_pending_at = _parse_oldest_pending_at(d.pop("oldest_pending_at", UNSET))

        def _parse_oldest_pending_age_seconds(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        oldest_pending_age_seconds = _parse_oldest_pending_age_seconds(d.pop("oldest_pending_age_seconds", UNSET))

        queue_state_response = cls(
            app_slug=app_slug,
            plan=plan,
            plan_cap=plan_cap,
            depth=depth,
            in_flight=in_flight,
            generated_at=generated_at,
            oldest_pending_at=oldest_pending_at,
            oldest_pending_age_seconds=oldest_pending_age_seconds,
        )

        queue_state_response.additional_properties = d
        return queue_state_response

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
