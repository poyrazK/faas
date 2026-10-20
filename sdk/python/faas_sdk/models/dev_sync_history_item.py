from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.dev_sync_history_item_status import DevSyncHistoryItemStatus, check_dev_sync_history_item_status

if TYPE_CHECKING:
    from ..models.dev_sync_phase import DevSyncPhase


T = TypeVar("T", bound="DevSyncHistoryItem")


@_attrs_define
class DevSyncHistoryItem:
    """One redacted developer sync receipt."""

    deployment_id: str
    status: DevSyncHistoryItemStatus
    edit_to_live_ms: int
    slo_target_ms: int
    within_slo: bool
    phases: list[DevSyncPhase]
    created_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = self.deployment_id

        status: str = self.status

        edit_to_live_ms = self.edit_to_live_ms

        slo_target_ms = self.slo_target_ms

        within_slo = self.within_slo

        phases = []
        for phases_item_data in self.phases:
            phases_item = phases_item_data.to_dict()
            phases.append(phases_item)

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "status": status,
                "edit_to_live_ms": edit_to_live_ms,
                "slo_target_ms": slo_target_ms,
                "within_slo": within_slo,
                "phases": phases,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.dev_sync_phase import DevSyncPhase

        d = dict(src_dict)
        deployment_id = d.pop("deployment_id")

        status = check_dev_sync_history_item_status(d.pop("status"))

        edit_to_live_ms = d.pop("edit_to_live_ms")

        slo_target_ms = d.pop("slo_target_ms")

        within_slo = d.pop("within_slo")

        phases = []
        _phases = d.pop("phases")
        for phases_item_data in _phases:
            phases_item = DevSyncPhase.from_dict(phases_item_data)

            phases.append(phases_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        dev_sync_history_item = cls(
            deployment_id=deployment_id,
            status=status,
            edit_to_live_ms=edit_to_live_ms,
            slo_target_ms=slo_target_ms,
            within_slo=within_slo,
            phases=phases,
            created_at=created_at,
        )

        dev_sync_history_item.additional_properties = d
        return dev_sync_history_item

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
