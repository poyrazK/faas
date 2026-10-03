from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.commit_operation_response_state import CommitOperationResponseState, check_commit_operation_response_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="CommitOperationResponse")


@_attrs_define
class CommitOperationResponse:
    """Retained Commit acceptance and execution facts after managed Operation cleanup."""

    id: UUID
    receipt_id: UUID
    source_id: UUID
    event_id: UUID
    state: CommitOperationResponseState
    accepted_at: datetime.datetime
    completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        receipt_id = str(self.receipt_id)

        source_id = str(self.source_id)

        event_id = str(self.event_id)

        state: str = self.state

        accepted_at = self.accepted_at.isoformat()

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "receipt_id": receipt_id,
                "source_id": source_id,
                "event_id": event_id,
                "state": state,
                "accepted_at": accepted_at,
            }
        )
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        receipt_id = UUID(d.pop("receipt_id"))

        source_id = UUID(d.pop("source_id"))

        event_id = UUID(d.pop("event_id"))

        state = check_commit_operation_response_state(d.pop("state"))

        accepted_at = datetime.datetime.fromisoformat(d.pop("accepted_at"))

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        commit_operation_response = cls(
            id=id,
            receipt_id=receipt_id,
            source_id=source_id,
            event_id=event_id,
            state=state,
            accepted_at=accepted_at,
            completed_at=completed_at,
        )

        commit_operation_response.additional_properties = d
        return commit_operation_response

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
