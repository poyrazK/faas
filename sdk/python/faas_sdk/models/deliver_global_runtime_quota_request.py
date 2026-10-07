from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="DeliverGlobalRuntimeQuotaRequest")


@_attrs_define
class DeliverGlobalRuntimeQuotaRequest:
    """Supply either a request-only regional account-admin key or the original active runtime consent UUID, never both."""

    regional_api_key: str | Unset = _attrs_field(default=UNSET, repr=False)
    runtime_consent_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        regional_api_key = self.regional_api_key

        runtime_consent_id: str | Unset = UNSET
        if not isinstance(self.runtime_consent_id, Unset):
            runtime_consent_id = str(self.runtime_consent_id)

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if regional_api_key is not UNSET:
            field_dict["regional_api_key"] = regional_api_key
        if runtime_consent_id is not UNSET:
            field_dict["runtime_consent_id"] = runtime_consent_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        regional_api_key = d.pop("regional_api_key", UNSET)

        _runtime_consent_id = d.pop("runtime_consent_id", UNSET)
        runtime_consent_id: UUID | Unset
        if isinstance(_runtime_consent_id, Unset):
            runtime_consent_id = UNSET
        else:
            runtime_consent_id = UUID(_runtime_consent_id)

        deliver_global_runtime_quota_request = cls(
            regional_api_key=regional_api_key,
            runtime_consent_id=runtime_consent_id,
        )

        return deliver_global_runtime_quota_request
