from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="InspectFeatureFlagRequest")


@_attrs_define
class InspectFeatureFlagRequest:
    """Owner-authorized simulation context; does not record exposure."""

    customer_id: UUID | Unset = UNSET
    """Omit for anonymous evaluation."""
    version: int | Unset = UNSET
    """Zero or omitted selects current configuration."""
    fallback: bool | Unset = False

    def to_dict(self) -> dict[str, Any]:
        customer_id: str | Unset = UNSET
        if not isinstance(self.customer_id, Unset):
            customer_id = str(self.customer_id)

        version = self.version

        fallback = self.fallback

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if customer_id is not UNSET:
            field_dict["customer_id"] = customer_id
        if version is not UNSET:
            field_dict["version"] = version
        if fallback is not UNSET:
            field_dict["fallback"] = fallback

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _customer_id = d.pop("customer_id", UNSET)
        customer_id: UUID | Unset
        if isinstance(_customer_id, Unset):
            customer_id = UNSET
        else:
            customer_id = UUID(_customer_id)

        version = d.pop("version", UNSET)

        fallback = d.pop("fallback", UNSET)

        inspect_feature_flag_request = cls(
            customer_id=customer_id,
            version=version,
            fallback=fallback,
        )

        return inspect_feature_flag_request
