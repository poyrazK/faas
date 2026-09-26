from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.discovered_routes_response_source import (
    DiscoveredRoutesResponseSource,
    check_discovered_routes_response_source,
)

if TYPE_CHECKING:
    from ..models.discovered_api_route import DiscoveredAPIRoute


T = TypeVar("T", bound="DiscoveredRoutesResponse")


@_attrs_define
class DiscoveredRoutesResponse:
    """Bounded API route inventory independent of exact audit retention."""

    app_id: UUID
    routes: list[DiscoveredAPIRoute]
    cap_hit: bool
    """True once new candidates overflow the 500-route cap."""
    source: DiscoveredRoutesResponseSource
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        routes = []
        for routes_item_data in self.routes:
            routes_item = routes_item_data.to_dict()
            routes.append(routes_item)

        cap_hit = self.cap_hit

        source: str = self.source

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "routes": routes,
                "cap_hit": cap_hit,
                "source": source,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.discovered_api_route import DiscoveredAPIRoute

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        routes = []
        _routes = d.pop("routes")
        for routes_item_data in _routes:
            routes_item = DiscoveredAPIRoute.from_dict(routes_item_data)

            routes.append(routes_item)

        cap_hit = d.pop("cap_hit")

        source = check_discovered_routes_response_source(d.pop("source"))

        discovered_routes_response = cls(
            app_id=app_id,
            routes=routes,
            cap_hit=cap_hit,
            source=source,
        )

        discovered_routes_response.additional_properties = d
        return discovered_routes_response

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
