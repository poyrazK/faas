from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.object_signed_request_method import ObjectSignedRequestMethod, check_object_signed_request_method

if TYPE_CHECKING:
    from ..models.object_signed_request_headers import ObjectSignedRequestHeaders


T = TypeVar("T", bound="ObjectSignedRequest")


@_attrs_define
class ObjectSignedRequest:
    """Temporary bearer capability for a direct provider request. Do not log or persist it."""

    url: str
    method: ObjectSignedRequestMethod
    headers: ObjectSignedRequestHeaders
    expires_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        url = self.url

        method: str = self.method

        headers = self.headers.to_dict()

        expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "url": url,
                "method": method,
                "headers": headers,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_signed_request_headers import ObjectSignedRequestHeaders

        d = dict(src_dict)
        url = d.pop("url")

        method = check_object_signed_request_method(d.pop("method"))

        headers = ObjectSignedRequestHeaders.from_dict(d.pop("headers"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        object_signed_request = cls(
            url=url,
            method=method,
            headers=headers,
            expires_at=expires_at,
        )

        object_signed_request.additional_properties = d
        return object_signed_request

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
