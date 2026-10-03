from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="AttachGlobalAppTargetRequest")


@_attrs_define
class AttachGlobalAppTargetRequest:
    """One-request proof of regional ownership. The key is never retained or returned. Body is bounded to 4096 bytes."""

    regional_app_slug: str
    regional_api_key: str = _attrs_field(repr=False)
    """Existing regional personal account-admin API key used only for this verification."""

    def to_dict(self) -> dict[str, Any]:
        regional_app_slug = self.regional_app_slug

        regional_api_key = self.regional_api_key

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "regional_app_slug": regional_app_slug,
                "regional_api_key": regional_api_key,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        regional_app_slug = d.pop("regional_app_slug")

        regional_api_key = d.pop("regional_api_key")

        attach_global_app_target_request = cls(
            regional_app_slug=regional_app_slug,
            regional_api_key=regional_api_key,
        )

        return attach_global_app_target_request
