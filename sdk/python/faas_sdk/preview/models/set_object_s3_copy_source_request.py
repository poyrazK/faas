from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="SetObjectS3CopySourceRequest")


@_attrs_define
class SetObjectS3CopySourceRequest:
    """Exact source key prefix. Empty or omitted prefix grants all keys in the owned source bucket solely for copy."""

    prefix: str | Unset = ""

    def to_dict(self) -> dict[str, Any]:
        prefix = self.prefix

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if prefix is not UNSET:
            field_dict["prefix"] = prefix

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        prefix = d.pop("prefix", UNSET)

        set_object_s3_copy_source_request = cls(
            prefix=prefix,
        )

        return set_object_s3_copy_source_request
