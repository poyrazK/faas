from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RevokeGlobalRuntimeConsentRequest")


@_attrs_define
class RevokeGlobalRuntimeConsentRequest:
    """Request-only regional owner credential to revoke the original runtime consent."""

    regional_api_key: str = _attrs_field(repr=False)

    def to_dict(self) -> dict[str, Any]:
        regional_api_key = self.regional_api_key

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "regional_api_key": regional_api_key,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        regional_api_key = d.pop("regional_api_key")

        revoke_global_runtime_consent_request = cls(
            regional_api_key=regional_api_key,
        )

        return revoke_global_runtime_consent_request
