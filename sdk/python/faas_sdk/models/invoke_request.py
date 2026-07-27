from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.invoke_request_headers import InvokeRequestHeaders
    from ..models.invoke_request_payload import InvokeRequestPayload


T = TypeVar("T", bound="InvokeRequest")


@_attrs_define
class InvokeRequest:
    """Body for POST /v1/apps/{slug}/invoke[/async]. Method defaults to POST; path defaults to `/`."""

    payload: InvokeRequestPayload | Unset = UNSET
    headers: InvokeRequestHeaders | Unset = UNSET
    method: str | Unset = "POST"
    path: str | Unset = "/"
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        payload: dict[str, Any] | Unset = UNSET
        if not isinstance(self.payload, Unset):
            payload = self.payload.to_dict()

        headers: dict[str, Any] | Unset = UNSET
        if not isinstance(self.headers, Unset):
            headers = self.headers.to_dict()

        method = self.method

        path = self.path

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if payload is not UNSET:
            field_dict["payload"] = payload
        if headers is not UNSET:
            field_dict["headers"] = headers
        if method is not UNSET:
            field_dict["method"] = method
        if path is not UNSET:
            field_dict["path"] = path

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.invoke_request_headers import InvokeRequestHeaders
        from ..models.invoke_request_payload import InvokeRequestPayload

        d = dict(src_dict)
        _payload = d.pop("payload", UNSET)
        payload: InvokeRequestPayload | Unset
        if isinstance(_payload, Unset):
            payload = UNSET
        else:
            payload = InvokeRequestPayload.from_dict(_payload)

        _headers = d.pop("headers", UNSET)
        headers: InvokeRequestHeaders | Unset
        if isinstance(_headers, Unset):
            headers = UNSET
        else:
            headers = InvokeRequestHeaders.from_dict(_headers)

        method = d.pop("method", UNSET)

        path = d.pop("path", UNSET)

        invoke_request = cls(
            payload=payload,
            headers=headers,
            method=method,
            path=path,
        )

        invoke_request.additional_properties = d
        return invoke_request

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
