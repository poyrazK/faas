from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="RouteHealthProbe")


@_attrs_define
class RouteHealthProbe:
    """Opt-in synthetic probe for a GET or HEAD selector (ADR-847). While a canary is in flight and organic evidence stays
    sparse, Gregale sends a few bodyless requests per minute to the candidate and stable deployments with customer auth
    gates unchanged. Probe requests never appear in request telemetry, analytics or usage; they wake the app like any
    request. At most 5 selectors per app.

    """

    path: str
    """Concrete absolute path matching the selector, with a value for each {parameter}, for example /users/42 for
    /users/{id}. No query, fragment or wildcard."""

    def to_dict(self) -> dict[str, Any]:
        path = self.path

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "path": path,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        path = d.pop("path")

        route_health_probe = cls(
            path=path,
        )

        return route_health_probe
