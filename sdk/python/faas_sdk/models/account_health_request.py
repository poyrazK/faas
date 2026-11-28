from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AccountHealthRequest")


@_attrs_define
class AccountHealthRequest:
    """One refused request: the matched route pattern, the problem code and the ids to quote to support."""

    occurred_at: datetime.datetime
    """When the request was refused."""
    route: str
    """Matched route pattern, for example POST /v1/apps/{slug}/deployments; never the raw URL."""
    code: str
    """Problem code returned."""
    app_slug: str | Unset = UNSET
    """App slug from the request path, when the route has one."""
    request_id: str | Unset = UNSET
    """X-Faas-Request-Id of the request."""
    trace_id: str | Unset = UNSET
    """X-Gregale-Trace-Id of the request."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        occurred_at = self.occurred_at.isoformat()

        route = self.route

        code = self.code

        app_slug = self.app_slug

        request_id = self.request_id

        trace_id = self.trace_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "occurred_at": occurred_at,
                "route": route,
                "code": code,
            }
        )
        if app_slug is not UNSET:
            field_dict["app_slug"] = app_slug
        if request_id is not UNSET:
            field_dict["request_id"] = request_id
        if trace_id is not UNSET:
            field_dict["trace_id"] = trace_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        route = d.pop("route")

        code = d.pop("code")

        app_slug = d.pop("app_slug", UNSET)

        request_id = d.pop("request_id", UNSET)

        trace_id = d.pop("trace_id", UNSET)

        account_health_request = cls(
            occurred_at=occurred_at,
            route=route,
            code=code,
            app_slug=app_slug,
            request_id=request_id,
            trace_id=trace_id,
        )

        account_health_request.additional_properties = d
        return account_health_request

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
