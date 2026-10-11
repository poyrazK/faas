from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operation_summary_state import OperationSummaryState, check_operation_summary_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_delivery_summary import OperationDeliverySummary
    from ..models.operation_progress import OperationProgress


T = TypeVar("T", bound="OperationSummary")


@_attrs_define
class OperationSummary:
    """Bounded discovery metadata; omits result bytes, artifact locations and execution authority."""

    id: UUID
    name: str
    generation: int
    state: OperationSummaryState
    completion_delivery: OperationDeliverySummary
    """Independent notification status without delivery identifiers or errors."""
    cancellation_requested: bool
    latest_sequence: int
    created_at: datetime.datetime
    updated_at: datetime.datetime
    expires_at: datetime.datetime
    platform_tenant_id: UUID | Unset = UNSET
    """Present only on account operator listings; absent from tenant-self summaries."""
    progress: OperationProgress | Unset = UNSET
    """Current bounded progress from the active execution attempt."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        name = self.name

        generation = self.generation

        state: str = self.state

        completion_delivery = self.completion_delivery.to_dict()

        cancellation_requested = self.cancellation_requested

        latest_sequence = self.latest_sequence

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        expires_at = self.expires_at.isoformat()

        platform_tenant_id: str | Unset = UNSET
        if not isinstance(self.platform_tenant_id, Unset):
            platform_tenant_id = str(self.platform_tenant_id)

        progress: dict[str, Any] | Unset = UNSET
        if not isinstance(self.progress, Unset):
            progress = self.progress.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "generation": generation,
                "state": state,
                "completion_delivery": completion_delivery,
                "cancellation_requested": cancellation_requested,
                "latest_sequence": latest_sequence,
                "created_at": created_at,
                "updated_at": updated_at,
                "expires_at": expires_at,
            }
        )
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id
        if progress is not UNSET:
            field_dict["progress"] = progress

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_delivery_summary import OperationDeliverySummary
        from ..models.operation_progress import OperationProgress

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        generation = d.pop("generation")

        state = check_operation_summary_state(d.pop("state"))

        completion_delivery = OperationDeliverySummary.from_dict(d.pop("completion_delivery"))

        cancellation_requested = d.pop("cancellation_requested")

        latest_sequence = d.pop("latest_sequence")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        _platform_tenant_id = d.pop("platform_tenant_id", UNSET)
        platform_tenant_id: UUID | Unset
        if isinstance(_platform_tenant_id, Unset):
            platform_tenant_id = UNSET
        else:
            platform_tenant_id = UUID(_platform_tenant_id)

        _progress = d.pop("progress", UNSET)
        progress: OperationProgress | Unset
        if isinstance(_progress, Unset):
            progress = UNSET
        else:
            progress = OperationProgress.from_dict(_progress)

        operation_summary = cls(
            id=id,
            name=name,
            generation=generation,
            state=state,
            completion_delivery=completion_delivery,
            cancellation_requested=cancellation_requested,
            latest_sequence=latest_sequence,
            created_at=created_at,
            updated_at=updated_at,
            expires_at=expires_at,
            platform_tenant_id=platform_tenant_id,
            progress=progress,
        )

        operation_summary.additional_properties = d
        return operation_summary

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
