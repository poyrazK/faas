from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.profile_query import ProfileQuery
    from ..models.profile_request_mix_group import ProfileRequestMixGroup


T = TypeVar("T", bound="ProfileRequestMixWindow")


@_attrs_define
class ProfileRequestMixWindow:
    """Frozen request-count summary for one profile query window."""

    query: ProfileQuery
    """Authorized deployment CPU capture window."""
    total: int
    routes: list[ProfileRequestMixGroup]
    statuses: list[ProfileRequestMixGroup]
    truncated: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        query = self.query.to_dict()

        total = self.total

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        statuses = []
        for statuses_item_data in self.statuses:
            statuses_item = statuses_item_data.to_dict()
            statuses.append(statuses_item)

        truncated = self.truncated

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "query": query,
                "total": total,
                "routes": routes,
                "statuses": statuses,
                "truncated": truncated,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profile_query import ProfileQuery
        from ..models.profile_request_mix_group import ProfileRequestMixGroup

        d = dict(src_dict)
        query = ProfileQuery.from_dict(d.pop("query"))

        total = d.pop("total")

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = ProfileRequestMixGroup.from_dict(routes_item_data)

            routes.append(routes_item)

        statuses = []
        _statuses = d.pop("statuses")
        for statuses_item_data in _statuses:
            statuses_item = ProfileRequestMixGroup.from_dict(statuses_item_data)

            statuses.append(statuses_item)

        truncated = d.pop("truncated")

        profile_request_mix_window = cls(
            query=query,
            total=total,
            routes=routes,
            statuses=statuses,
            truncated=truncated,
        )

        profile_request_mix_window.additional_properties = d
        return profile_request_mix_window

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
