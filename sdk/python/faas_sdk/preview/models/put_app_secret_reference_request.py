from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="PutAppSecretReferenceRequest")


@_attrs_define
class PutAppSecretReferenceRequest:
    """Named sealed source selected within the exact registered project environment; contains no secret value."""

    reference: str

    def to_dict(self) -> dict[str, Any]:
        reference = self.reference

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "reference": reference,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        reference = d.pop("reference")

        put_app_secret_reference_request = cls(
            reference=reference,
        )

        return put_app_secret_reference_request
