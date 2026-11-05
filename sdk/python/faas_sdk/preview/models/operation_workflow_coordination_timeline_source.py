from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="OperationWorkflowCoordinationTimelineSource")


@_attrs_define
class OperationWorkflowCoordinationTimelineSource:
    """OperationWorkflowCoordinationTimelineSource. Retained inbox coordination projection metadata."""

    subscription_id: UUID
    """OperationWorkflowCoordinationTimelineSource.subscription_id. Read subscription UUID."""
    name: str
    """OperationWorkflowCoordinationTimelineSource.name. Current subscription name."""
    earliest_sequence: int
    """OperationWorkflowCoordinationTimelineSource.earliest_sequence. Earliest retained sequence, or zero when
    empty."""
    latest_sequence: int
    """OperationWorkflowCoordinationTimelineSource.latest_sequence. Latest published sequence at source read."""
    retained_count: int
    """OperationWorkflowCoordinationTimelineSource.retained_count. Notifications retained in this source, including
    other workflow identities."""
    gap_before: bool
    """OperationWorkflowCoordinationTimelineSource.gap_before. Older notifications were pruned or an inbox
    resynchronization is required."""
    missing_during_read: bool
    """OperationWorkflowCoordinationTimelineSource.missing_during_read. Subscription disappeared between listing
    and reading."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        subscription_id = str(self.subscription_id)

        name = self.name

        earliest_sequence = self.earliest_sequence

        latest_sequence = self.latest_sequence

        retained_count = self.retained_count

        gap_before = self.gap_before

        missing_during_read = self.missing_during_read

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "subscription_id": subscription_id,
                "name": name,
                "earliest_sequence": earliest_sequence,
                "latest_sequence": latest_sequence,
                "retained_count": retained_count,
                "gap_before": gap_before,
                "missing_during_read": missing_during_read,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        subscription_id = UUID(d.pop("subscription_id"))

        name = d.pop("name")

        earliest_sequence = d.pop("earliest_sequence")

        latest_sequence = d.pop("latest_sequence")

        retained_count = d.pop("retained_count")

        gap_before = d.pop("gap_before")

        missing_during_read = d.pop("missing_during_read")

        operation_workflow_coordination_timeline_source = cls(
            subscription_id=subscription_id,
            name=name,
            earliest_sequence=earliest_sequence,
            latest_sequence=latest_sequence,
            retained_count=retained_count,
            gap_before=gap_before,
            missing_during_read=missing_during_read,
        )

        operation_workflow_coordination_timeline_source.additional_properties = d
        return operation_workflow_coordination_timeline_source

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
