from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.record_dev_sync_request_status import RecordDevSyncRequestStatus, check_record_dev_sync_request_status

if TYPE_CHECKING:
    from ..models.dev_sync_phase import DevSyncPhase


T = TypeVar("T", bound="RecordDevSyncRequest")


@_attrs_define
class RecordDevSyncRequest:
    """Redacted edit-to-live receipt written by the developer CLI."""

    workspace_id: str
    deployment_id: str
    status: RecordDevSyncRequestStatus
    edit_to_live_ms: int
    slo_target_ms: int
    within_slo: bool
    phases: list[DevSyncPhase]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        workspace_id = self.workspace_id

        deployment_id = self.deployment_id

        status: str = self.status

        edit_to_live_ms = self.edit_to_live_ms

        slo_target_ms = self.slo_target_ms

        within_slo = self.within_slo

        phases = []
        for phases_item_data in self.phases:
            phases_item = phases_item_data.to_dict()
            phases.append(phases_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "workspace_id": workspace_id,
                "deployment_id": deployment_id,
                "status": status,
                "edit_to_live_ms": edit_to_live_ms,
                "slo_target_ms": slo_target_ms,
                "within_slo": within_slo,
                "phases": phases,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.dev_sync_phase import DevSyncPhase

        d = dict(src_dict)
        workspace_id = d.pop("workspace_id")

        deployment_id = d.pop("deployment_id")

        status = check_record_dev_sync_request_status(d.pop("status"))

        edit_to_live_ms = d.pop("edit_to_live_ms")

        slo_target_ms = d.pop("slo_target_ms")

        within_slo = d.pop("within_slo")

        phases = []
        _phases = d.pop("phases")
        for phases_item_data in _phases:
            phases_item = DevSyncPhase.from_dict(phases_item_data)

            phases.append(phases_item)

        record_dev_sync_request = cls(
            workspace_id=workspace_id,
            deployment_id=deployment_id,
            status=status,
            edit_to_live_ms=edit_to_live_ms,
            slo_target_ms=slo_target_ms,
            within_slo=within_slo,
            phases=phases,
        )

        record_dev_sync_request.additional_properties = d
        return record_dev_sync_request

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
