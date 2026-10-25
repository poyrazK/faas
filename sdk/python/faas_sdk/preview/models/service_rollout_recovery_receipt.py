from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.service_rollout_recovery_receipt_status import (
    ServiceRolloutRecoveryReceiptStatus,
    check_service_rollout_recovery_receipt_status,
)

T = TypeVar("T", bound="ServiceRolloutRecoveryReceipt")


@_attrs_define
class ServiceRolloutRecoveryReceipt:
    """Durable exact service abort request. Acceptance does not confirm traffic restoration, gateway acknowledgement or
    request drain. Poll the exact deployment's service_rollout_handoff.

    """

    deployment_id: UUID
    predecessor_deployment_id: UUID
    request_id: UUID
    status: ServiceRolloutRecoveryReceiptStatus
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        predecessor_deployment_id = str(self.predecessor_deployment_id)

        request_id = str(self.request_id)

        status: str = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "predecessor_deployment_id": predecessor_deployment_id,
                "request_id": request_id,
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        predecessor_deployment_id = UUID(d.pop("predecessor_deployment_id"))

        request_id = UUID(d.pop("request_id"))

        status = check_service_rollout_recovery_receipt_status(d.pop("status"))

        service_rollout_recovery_receipt = cls(
            deployment_id=deployment_id,
            predecessor_deployment_id=predecessor_deployment_id,
            request_id=request_id,
            status=status,
        )

        service_rollout_recovery_receipt.additional_properties = d
        return service_rollout_recovery_receipt

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
