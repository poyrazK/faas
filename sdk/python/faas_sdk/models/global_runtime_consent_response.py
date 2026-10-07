from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.global_runtime_consent_response_status import (
    GlobalRuntimeConsentResponseStatus,
    check_global_runtime_consent_response_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.global_runtime_consent_binding_response import GlobalRuntimeConsentBindingResponse


T = TypeVar("T", bound="GlobalRuntimeConsentResponse")


@_attrs_define
class GlobalRuntimeConsentResponse:
    """Original directory consent and regional observation. Personal targets only; not runtime admission or billing
    authority.

    """

    id: UUID
    issuer_id: UUID
    global_app_id: UUID
    owner_id: UUID
    global_app_slug: str
    binding: GlobalRuntimeConsentBindingResponse
    """Original verified personal regional binding retained by directory consent and delivery."""
    expires_at: datetime.datetime
    created_at: datetime.datetime
    status: GlobalRuntimeConsentResponseStatus
    regional_created_at: datetime.datetime | Unset = UNSET
    regional_revoked_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        issuer_id = str(self.issuer_id)

        global_app_id = str(self.global_app_id)

        owner_id = str(self.owner_id)

        global_app_slug = self.global_app_slug

        binding = self.binding.to_dict()

        expires_at = self.expires_at.isoformat()

        created_at = self.created_at.isoformat()

        status: str = self.status

        regional_created_at: str | Unset = UNSET
        if not isinstance(self.regional_created_at, Unset):
            regional_created_at = self.regional_created_at.isoformat()

        regional_revoked_at: str | Unset = UNSET
        if not isinstance(self.regional_revoked_at, Unset):
            regional_revoked_at = self.regional_revoked_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "issuer_id": issuer_id,
                "global_app_id": global_app_id,
                "owner_id": owner_id,
                "global_app_slug": global_app_slug,
                "binding": binding,
                "expires_at": expires_at,
                "created_at": created_at,
                "status": status,
            }
        )
        if regional_created_at is not UNSET:
            field_dict["regional_created_at"] = regional_created_at
        if regional_revoked_at is not UNSET:
            field_dict["regional_revoked_at"] = regional_revoked_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.global_runtime_consent_binding_response import GlobalRuntimeConsentBindingResponse

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        issuer_id = UUID(d.pop("issuer_id"))

        global_app_id = UUID(d.pop("global_app_id"))

        owner_id = UUID(d.pop("owner_id"))

        global_app_slug = d.pop("global_app_slug")

        binding = GlobalRuntimeConsentBindingResponse.from_dict(d.pop("binding"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        status = check_global_runtime_consent_response_status(d.pop("status"))

        _regional_created_at = d.pop("regional_created_at", UNSET)
        regional_created_at: datetime.datetime | Unset
        if isinstance(_regional_created_at, Unset):
            regional_created_at = UNSET
        else:
            regional_created_at = datetime.datetime.fromisoformat(_regional_created_at)

        _regional_revoked_at = d.pop("regional_revoked_at", UNSET)
        regional_revoked_at: datetime.datetime | Unset
        if isinstance(_regional_revoked_at, Unset):
            regional_revoked_at = UNSET
        else:
            regional_revoked_at = datetime.datetime.fromisoformat(_regional_revoked_at)

        global_runtime_consent_response = cls(
            id=id,
            issuer_id=issuer_id,
            global_app_id=global_app_id,
            owner_id=owner_id,
            global_app_slug=global_app_slug,
            binding=binding,
            expires_at=expires_at,
            created_at=created_at,
            status=status,
            regional_created_at=regional_created_at,
            regional_revoked_at=regional_revoked_at,
        )

        global_runtime_consent_response.additional_properties = d
        return global_runtime_consent_response

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
