from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.queue_global_quota_delivery_request_purpose import (
    QueueGlobalQuotaDeliveryRequestPurpose,
    check_queue_global_quota_delivery_request_purpose,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="QueueGlobalQuotaDeliveryRequest")


@_attrs_define
class QueueGlobalQuotaDeliveryRequest:
    """Immutable selected region, delivery purpose and original consent for durable dispatch."""

    region: str
    purpose: QueueGlobalQuotaDeliveryRequestPurpose
    runtime_consent_id: UUID | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        region = self.region

        purpose: str = self.purpose

        runtime_consent_id: str | Unset = UNSET
        if not isinstance(self.runtime_consent_id, Unset):
            runtime_consent_id = str(self.runtime_consent_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "region": region,
                "purpose": purpose,
            }
        )
        if runtime_consent_id is not UNSET:
            field_dict["runtime_consent_id"] = runtime_consent_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        region = d.pop("region")

        purpose = check_queue_global_quota_delivery_request_purpose(d.pop("purpose"))

        _runtime_consent_id = d.pop("runtime_consent_id", UNSET)
        runtime_consent_id: UUID | Unset
        if isinstance(_runtime_consent_id, Unset):
            runtime_consent_id = UNSET
        else:
            runtime_consent_id = UUID(_runtime_consent_id)

        queue_global_quota_delivery_request = cls(
            region=region,
            purpose=purpose,
            runtime_consent_id=runtime_consent_id,
        )

        return queue_global_quota_delivery_request
