from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProfileQuery")


@_attrs_define
class ProfileQuery:
    """Authorized deployment CPU capture window."""

    deployment_id: UUID
    runtime: str
    start: datetime.datetime
    end: datetime.datetime
    route: str | Unset = UNSET
    """Optional static METHOD /pattern or [unattributed]; empty selects all sampled CPU."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        runtime = self.runtime

        start = self.start.isoformat()

        end = self.end.isoformat()

        route = self.route

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "runtime": runtime,
                "start": start,
                "end": end,
            }
        )
        if route is not UNSET:
            field_dict["route"] = route

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        runtime = d.pop("runtime")

        start = datetime.datetime.fromisoformat(d.pop("start"))

        end = datetime.datetime.fromisoformat(d.pop("end"))

        route = d.pop("route", UNSET)

        profile_query = cls(
            deployment_id=deployment_id,
            runtime=runtime,
            start=start,
            end=end,
            route=route,
        )

        profile_query.additional_properties = d
        return profile_query

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
