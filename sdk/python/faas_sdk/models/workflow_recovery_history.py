from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_recovery_history_entry import WorkflowRecoveryHistoryEntry


T = TypeVar("T", bound="WorkflowRecoveryHistory")


@_attrs_define
class WorkflowRecoveryHistory:
    """A bounded page of retained recovery evidence with a fixed observation window."""

    observed_at: datetime.datetime
    created_after: datetime.datetime
    created_before: datetime.datetime
    entries: list[WorkflowRecoveryHistoryEntry]
    retained_observations: bool
    """True indicates retained evidence rather than a complete immutable audit log."""
    next_after: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        created_after = self.created_after.isoformat()

        created_before = self.created_before.isoformat()

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        retained_observations = self.retained_observations

        next_after = self.next_after

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "observed_at": observed_at,
                "created_after": created_after,
                "created_before": created_before,
                "entries": entries,
                "retained_observations": retained_observations,
            }
        )
        if next_after is not UNSET:
            field_dict["next_after"] = next_after

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_recovery_history_entry import WorkflowRecoveryHistoryEntry

        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        created_after = datetime.datetime.fromisoformat(d.pop("created_after"))

        created_before = datetime.datetime.fromisoformat(d.pop("created_before"))

        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = WorkflowRecoveryHistoryEntry.from_dict(entries_item_data)

            entries.append(entries_item)

        retained_observations = d.pop("retained_observations")

        next_after = d.pop("next_after", UNSET)

        workflow_recovery_history = cls(
            observed_at=observed_at,
            created_after=created_after,
            created_before=created_before,
            entries=entries,
            retained_observations=retained_observations,
            next_after=next_after,
        )

        workflow_recovery_history.additional_properties = d
        return workflow_recovery_history

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
