from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_route_split_request_kind import CreateRouteSplitRequestKind, check_create_route_split_request_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateRouteSplitRequest")


@_attrs_define
class CreateRouteSplitRequest:
    """The split the route-resources read suggested, repeated so a split that changed since is refused."""

    kind: CreateRouteSplitRequestKind
    route: str
    route_ram_mb: int
    main_ram_mb: int
    method: str | Unset = UNSET
    since: str | Unset = UNSET
    """The analysed window, as for the route-resources read (default 24h)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        route = self.route

        route_ram_mb = self.route_ram_mb

        main_ram_mb = self.main_ram_mb

        method = self.method

        since = self.since

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "route": route,
                "route_ram_mb": route_ram_mb,
                "main_ram_mb": main_ram_mb,
            }
        )
        if method is not UNSET:
            field_dict["method"] = method
        if since is not UNSET:
            field_dict["since"] = since

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_create_route_split_request_kind(d.pop("kind"))

        route = d.pop("route")

        route_ram_mb = d.pop("route_ram_mb")

        main_ram_mb = d.pop("main_ram_mb")

        method = d.pop("method", UNSET)

        since = d.pop("since", UNSET)

        create_route_split_request = cls(
            kind=kind,
            route=route,
            route_ram_mb=route_ram_mb,
            main_ram_mb=main_ram_mb,
            method=method,
            since=since,
        )

        create_route_split_request.additional_properties = d
        return create_route_split_request

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
