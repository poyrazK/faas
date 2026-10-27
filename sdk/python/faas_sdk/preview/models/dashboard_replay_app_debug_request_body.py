from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DashboardReplayAppDebugRequestBody")


@_attrs_define
class DashboardReplayAppDebugRequestBody:
    csrf_token: str
    since: str | Unset = UNSET
    route: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        csrf_token = self.csrf_token

        since = self.since

        route = self.route

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "csrf_token": csrf_token,
            }
        )
        if since is not UNSET:
            field_dict["since"] = since
        if route is not UNSET:
            field_dict["route"] = route

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        csrf_token = d.pop("csrf_token")

        since = d.pop("since", UNSET)

        route = d.pop("route", UNSET)

        dashboard_replay_app_debug_request_body = cls(
            csrf_token=csrf_token,
            since=since,
            route=route,
        )

        dashboard_replay_app_debug_request_body.additional_properties = d
        return dashboard_replay_app_debug_request_body

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
