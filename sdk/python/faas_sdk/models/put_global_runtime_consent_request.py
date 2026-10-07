from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="PutGlobalRuntimeConsentRequest")


@_attrs_define
class PutGlobalRuntimeConsentRequest:
    """One request-only owner key and expiry for an original regional runtime consent."""

    region: str
    expires_at: datetime.datetime
    regional_api_key: str = _attrs_field(repr=False)

    def to_dict(self) -> dict[str, Any]:
        region = self.region

        expires_at = self.expires_at.isoformat()

        regional_api_key = self.regional_api_key

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "region": region,
                "expires_at": expires_at,
                "regional_api_key": regional_api_key,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        region = d.pop("region")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        regional_api_key = d.pop("regional_api_key")

        put_global_runtime_consent_request = cls(
            region=region,
            expires_at=expires_at,
            regional_api_key=regional_api_key,
        )

        return put_global_runtime_consent_request
