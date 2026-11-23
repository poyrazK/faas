from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectRollbackLiveTraffic")


@_attrs_define
class ProjectRollbackLiveTraffic:
    """Verification of configured 5xx budgets using production requests after an exact route generation was applied."""

    status: str
    """One of pending, awaiting_gateways, awaiting_traffic, insufficient_traffic, evidence_unavailable, passed, or
    failed."""
    reason: str | Unset = UNSET
    deadline: datetime.datetime | Unset = UNSET
    observe_after: datetime.datetime | Unset = UNSET
    """Earliest observation time requested when rechecking after resume."""
    checked_at: datetime.datetime | Unset = UNSET
    route_generation: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        status = self.status

        reason = self.reason

        deadline: str | Unset = UNSET
        if not isinstance(self.deadline, Unset):
            deadline = self.deadline.isoformat()

        observe_after: str | Unset = UNSET
        if not isinstance(self.observe_after, Unset):
            observe_after = self.observe_after.isoformat()

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        route_generation = self.route_generation

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "status": status,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if deadline is not UNSET:
            field_dict["deadline"] = deadline
        if observe_after is not UNSET:
            field_dict["observe_after"] = observe_after
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if route_generation is not UNSET:
            field_dict["route_generation"] = route_generation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = d.pop("status")

        reason = d.pop("reason", UNSET)

        _deadline = d.pop("deadline", UNSET)
        deadline: datetime.datetime | Unset
        if isinstance(_deadline, Unset):
            deadline = UNSET
        else:
            deadline = datetime.datetime.fromisoformat(_deadline)

        _observe_after = d.pop("observe_after", UNSET)
        observe_after: datetime.datetime | Unset
        if isinstance(_observe_after, Unset):
            observe_after = UNSET
        else:
            observe_after = datetime.datetime.fromisoformat(_observe_after)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        route_generation = d.pop("route_generation", UNSET)

        project_rollback_live_traffic = cls(
            status=status,
            reason=reason,
            deadline=deadline,
            observe_after=observe_after,
            checked_at=checked_at,
            route_generation=route_generation,
        )

        project_rollback_live_traffic.additional_properties = d
        return project_rollback_live_traffic

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
