from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.global_quota_delivery_response_purpose import (
    GlobalQuotaDeliveryResponsePurpose,
    check_global_quota_delivery_response_purpose,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.global_runtime_consent_binding_response import GlobalRuntimeConsentBindingResponse


T = TypeVar("T", bound="GlobalQuotaDeliveryResponse")


@_attrs_define
class GlobalQuotaDeliveryResponse:
    """Retained asynchronous delivery state. Accepted means a verified receipt was stored; it never means admission
    activated or native capacity drained.

    """

    id: UUID
    global_app_id: UUID
    allocation_id: UUID
    issuer_id: UUID
    generation: int
    purpose: GlobalQuotaDeliveryResponsePurpose
    binding: GlobalRuntimeConsentBindingResponse
    """Original verified personal regional binding retained by directory consent and delivery."""
    created_at: datetime.datetime
    status: str
    attempts: int
    next_attempt_at: datetime.datetime
    runtime_consent_id: UUID | Unset = UNSET
    retirement_id: UUID | Unset = UNSET
    claim_until: datetime.datetime | Unset = UNSET
    accepted_at: datetime.datetime | Unset = UNSET
    last_code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        global_app_id = str(self.global_app_id)

        allocation_id = str(self.allocation_id)

        issuer_id = str(self.issuer_id)

        generation = self.generation

        purpose: str = self.purpose

        binding = self.binding.to_dict()

        created_at = self.created_at.isoformat()

        status = self.status

        attempts = self.attempts

        next_attempt_at = self.next_attempt_at.isoformat()

        runtime_consent_id: str | Unset = UNSET
        if not isinstance(self.runtime_consent_id, Unset):
            runtime_consent_id = str(self.runtime_consent_id)

        retirement_id: str | Unset = UNSET
        if not isinstance(self.retirement_id, Unset):
            retirement_id = str(self.retirement_id)

        claim_until: str | Unset = UNSET
        if not isinstance(self.claim_until, Unset):
            claim_until = self.claim_until.isoformat()

        accepted_at: str | Unset = UNSET
        if not isinstance(self.accepted_at, Unset):
            accepted_at = self.accepted_at.isoformat()

        last_code = self.last_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "global_app_id": global_app_id,
                "allocation_id": allocation_id,
                "issuer_id": issuer_id,
                "generation": generation,
                "purpose": purpose,
                "binding": binding,
                "created_at": created_at,
                "status": status,
                "attempts": attempts,
                "next_attempt_at": next_attempt_at,
            }
        )
        if runtime_consent_id is not UNSET:
            field_dict["runtime_consent_id"] = runtime_consent_id
        if retirement_id is not UNSET:
            field_dict["retirement_id"] = retirement_id
        if claim_until is not UNSET:
            field_dict["claim_until"] = claim_until
        if accepted_at is not UNSET:
            field_dict["accepted_at"] = accepted_at
        if last_code is not UNSET:
            field_dict["last_code"] = last_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.global_runtime_consent_binding_response import GlobalRuntimeConsentBindingResponse

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        global_app_id = UUID(d.pop("global_app_id"))

        allocation_id = UUID(d.pop("allocation_id"))

        issuer_id = UUID(d.pop("issuer_id"))

        generation = d.pop("generation")

        purpose = check_global_quota_delivery_response_purpose(d.pop("purpose"))

        binding = GlobalRuntimeConsentBindingResponse.from_dict(d.pop("binding"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        status = d.pop("status")

        attempts = d.pop("attempts")

        next_attempt_at = datetime.datetime.fromisoformat(d.pop("next_attempt_at"))

        _runtime_consent_id = d.pop("runtime_consent_id", UNSET)
        runtime_consent_id: UUID | Unset
        if isinstance(_runtime_consent_id, Unset):
            runtime_consent_id = UNSET
        else:
            runtime_consent_id = UUID(_runtime_consent_id)

        _retirement_id = d.pop("retirement_id", UNSET)
        retirement_id: UUID | Unset
        if isinstance(_retirement_id, Unset):
            retirement_id = UNSET
        else:
            retirement_id = UUID(_retirement_id)

        _claim_until = d.pop("claim_until", UNSET)
        claim_until: datetime.datetime | Unset
        if isinstance(_claim_until, Unset):
            claim_until = UNSET
        else:
            claim_until = datetime.datetime.fromisoformat(_claim_until)

        _accepted_at = d.pop("accepted_at", UNSET)
        accepted_at: datetime.datetime | Unset
        if isinstance(_accepted_at, Unset):
            accepted_at = UNSET
        else:
            accepted_at = datetime.datetime.fromisoformat(_accepted_at)

        last_code = d.pop("last_code", UNSET)

        global_quota_delivery_response = cls(
            id=id,
            global_app_id=global_app_id,
            allocation_id=allocation_id,
            issuer_id=issuer_id,
            generation=generation,
            purpose=purpose,
            binding=binding,
            created_at=created_at,
            status=status,
            attempts=attempts,
            next_attempt_at=next_attempt_at,
            runtime_consent_id=runtime_consent_id,
            retirement_id=retirement_id,
            claim_until=claim_until,
            accepted_at=accepted_at,
            last_code=last_code,
        )

        global_quota_delivery_response.additional_properties = d
        return global_quota_delivery_response

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
