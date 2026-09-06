from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.object_sign_request_method import ObjectSignRequestMethod, check_object_sign_request_method
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectSignRequest")


@_attrs_define
class ObjectSignRequest:
    """Exact object operation to authorize for a short time."""

    method: ObjectSignRequestMethod
    key: str
    expires_in: int | Unset = 300
    size_bytes: int | Unset = UNSET
    """Required for PUT; forbidden for GET."""
    content_type: str | Unset = UNSET
    """PUT only."""

    def to_dict(self) -> dict[str, Any]:
        method: str = self.method

        key = self.key

        expires_in = self.expires_in

        size_bytes = self.size_bytes

        content_type = self.content_type

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "method": method,
                "key": key,
            }
        )
        if expires_in is not UNSET:
            field_dict["expires_in"] = expires_in
        if size_bytes is not UNSET:
            field_dict["size_bytes"] = size_bytes
        if content_type is not UNSET:
            field_dict["content_type"] = content_type

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        method = check_object_sign_request_method(d.pop("method"))

        key = d.pop("key")

        expires_in = d.pop("expires_in", UNSET)

        size_bytes = d.pop("size_bytes", UNSET)

        content_type = d.pop("content_type", UNSET)

        object_sign_request = cls(
            method=method,
            key=key,
            expires_in=expires_in,
            size_bytes=size_bytes,
            content_type=content_type,
        )

        return object_sign_request
