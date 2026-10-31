from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operator_restart_execution_response_phase import (
    OperatorRestartExecutionResponsePhase,
    check_operator_restart_execution_response_phase,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperatorRestartExecutionResponse")


@_attrs_define
class OperatorRestartExecutionResponse:
    """Durable restart evidence. An uncertain teardown is never automatically repeated. A later durable acknowledgement can
    resume database completion against the original target.

    """

    phase: OperatorRestartExecutionResponsePhase
    node_id: UUID
    wake_id: UUID
    generation: str
    snapshot_ids: list[UUID]
    updated_at: datetime.datetime
    destroyed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        phase: str = self.phase

        node_id = str(self.node_id)

        wake_id = str(self.wake_id)

        generation = self.generation

        snapshot_ids = []
        for snapshot_ids_item_data in self.snapshot_ids:
            snapshot_ids_item = str(snapshot_ids_item_data)
            snapshot_ids.append(snapshot_ids_item)

        updated_at = self.updated_at.isoformat()

        destroyed_at: str | Unset = UNSET
        if not isinstance(self.destroyed_at, Unset):
            destroyed_at = self.destroyed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "phase": phase,
                "node_id": node_id,
                "wake_id": wake_id,
                "generation": generation,
                "snapshot_ids": snapshot_ids,
                "updated_at": updated_at,
            }
        )
        if destroyed_at is not UNSET:
            field_dict["destroyed_at"] = destroyed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        phase = check_operator_restart_execution_response_phase(d.pop("phase"))

        node_id = UUID(d.pop("node_id"))

        wake_id = UUID(d.pop("wake_id"))

        generation = d.pop("generation")

        snapshot_ids = []
        _snapshot_ids = d.pop("snapshot_ids")
        for snapshot_ids_item_data in _snapshot_ids:
            snapshot_ids_item = UUID(snapshot_ids_item_data)

            snapshot_ids.append(snapshot_ids_item)

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _destroyed_at = d.pop("destroyed_at", UNSET)
        destroyed_at: datetime.datetime | Unset
        if isinstance(_destroyed_at, Unset):
            destroyed_at = UNSET
        else:
            destroyed_at = datetime.datetime.fromisoformat(_destroyed_at)

        operator_restart_execution_response = cls(
            phase=phase,
            node_id=node_id,
            wake_id=wake_id,
            generation=generation,
            snapshot_ids=snapshot_ids,
            updated_at=updated_at,
            destroyed_at=destroyed_at,
        )

        operator_restart_execution_response.additional_properties = d
        return operator_restart_execution_response

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
