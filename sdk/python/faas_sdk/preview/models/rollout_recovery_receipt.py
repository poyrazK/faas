from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.rollout_recovery_receipt_restored_traffic_percent import (
    RolloutRecoveryReceiptRestoredTrafficPercent,
    check_rollout_recovery_receipt_restored_traffic_percent,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.binding_check_report import BindingCheckReport


T = TypeVar("T", bound="RolloutRecoveryReceipt")


@_attrs_define
class RolloutRecoveryReceipt:
    """Committed exact canary abort, including the restored traffic recipient and any binding reports required by its
    stored release policy.

    """

    deployment_id: UUID
    predecessor_deployment_id: UUID
    restored_traffic_percent: RolloutRecoveryReceiptRestoredTrafficPercent
    bindings_checks: list[BindingCheckReport] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        predecessor_deployment_id = str(self.predecessor_deployment_id)

        restored_traffic_percent: int = self.restored_traffic_percent

        bindings_checks: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.bindings_checks, Unset):
            bindings_checks = []
            for bindings_checks_item_data in self.bindings_checks:
                bindings_checks_item = bindings_checks_item_data.to_dict()
                bindings_checks.append(bindings_checks_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "predecessor_deployment_id": predecessor_deployment_id,
                "restored_traffic_percent": restored_traffic_percent,
            }
        )
        if bindings_checks is not UNSET:
            field_dict["bindings_checks"] = bindings_checks

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_check_report import BindingCheckReport

        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        predecessor_deployment_id = UUID(d.pop("predecessor_deployment_id"))

        restored_traffic_percent = check_rollout_recovery_receipt_restored_traffic_percent(
            d.pop("restored_traffic_percent")
        )

        _bindings_checks = d.pop("bindings_checks", UNSET)
        bindings_checks: list[BindingCheckReport] | Unset = UNSET
        if _bindings_checks is not UNSET:
            bindings_checks = []
            for bindings_checks_item_data in _bindings_checks:
                bindings_checks_item = BindingCheckReport.from_dict(bindings_checks_item_data)

                bindings_checks.append(bindings_checks_item)

        rollout_recovery_receipt = cls(
            deployment_id=deployment_id,
            predecessor_deployment_id=predecessor_deployment_id,
            restored_traffic_percent=restored_traffic_percent,
            bindings_checks=bindings_checks,
        )

        rollout_recovery_receipt.additional_properties = d
        return rollout_recovery_receipt

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
