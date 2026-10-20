from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_error_request_item import AppErrorRequestItem


T = TypeVar("T", bound="AppErrorRequestsResponse")


@_attrs_define
class AppErrorRequestsResponse:
    """Drill-down page returned by `GET /v1/apps/{slug}/errors/{fingerprint}`
    (ADR-096 / PR-B). The header fields (fingerprint,
    error_class, route, http_status) are denormalised from the
    parent summary row so the dashboard renders the page header
    without a second round-trip. Does NOT include
    `headers_sample` or `redactions` — those are on the
    `/first` endpoint only.

    """

    fingerprint: str
    error_class: str
    route: str
    http_status: int
    requests: list[AppErrorRequestItem]
    next_cursor: None | str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        fingerprint = self.fingerprint

        error_class = self.error_class

        route = self.route

        http_status = self.http_status

        requests = []
        for requests_item_data in self.requests:
            requests_item = requests_item_data.to_dict()
            requests.append(requests_item)

        next_cursor: None | str | Unset
        if isinstance(self.next_cursor, Unset):
            next_cursor = UNSET
        else:
            next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "fingerprint": fingerprint,
                "error_class": error_class,
                "route": route,
                "http_status": http_status,
                "requests": requests,
            }
        )
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_error_request_item import AppErrorRequestItem

        d = dict(src_dict)
        fingerprint = d.pop("fingerprint")

        error_class = d.pop("error_class")

        route = d.pop("route")

        http_status = d.pop("http_status")

        requests = []
        _requests = d.pop("requests")
        for requests_item_data in _requests:
            requests_item = AppErrorRequestItem.from_dict(requests_item_data)

            requests.append(requests_item)

        def _parse_next_cursor(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        next_cursor = _parse_next_cursor(d.pop("next_cursor", UNSET))

        app_error_requests_response = cls(
            fingerprint=fingerprint,
            error_class=error_class,
            route=route,
            http_status=http_status,
            requests=requests,
            next_cursor=next_cursor,
        )

        app_error_requests_response.additional_properties = d
        return app_error_requests_response

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
