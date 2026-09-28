from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.pre_auth_route_limit_method import PreAuthRouteLimitMethod, check_pre_auth_route_limit_method

T = TypeVar("T", bound="PreAuthRouteLimit")


@_attrs_define
class PreAuthRouteLimit:
    """Optional stricter per-source limit for one public method and path."""

    method: PreAuthRouteLimitMethod
    path: str
    """Canonical absolute public URL path with no query, fragment, or percent encoding. Matching is exact after
    normalizing the decoded request path."""
    requests_per_second: int
    burst: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        method: str = self.method

        path = self.path

        requests_per_second = self.requests_per_second

        burst = self.burst

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "method": method,
                "path": path,
                "requests_per_second": requests_per_second,
                "burst": burst,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        method = check_pre_auth_route_limit_method(d.pop("method"))

        path = d.pop("path")

        requests_per_second = d.pop("requests_per_second")

        burst = d.pop("burst")

        pre_auth_route_limit = cls(
            method=method,
            path=path,
            requests_per_second=requests_per_second,
            burst=burst,
        )

        pre_auth_route_limit.additional_properties = d
        return pre_auth_route_limit

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
