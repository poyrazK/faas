from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectMultipartPartSignRequest")


@_attrs_define
class ObjectMultipartPartSignRequest:
    """Requested lifetime for one exact-length part capability."""

    expires_in: int | Unset = 300

    def to_dict(self) -> dict[str, Any]:
        expires_in = self.expires_in

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if expires_in is not UNSET:
            field_dict["expires_in"] = expires_in

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expires_in = d.pop("expires_in", UNSET)

        object_multipart_part_sign_request = cls(
            expires_in=expires_in,
        )

        return object_multipart_part_sign_request
