from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operator_park_execution_response_action import (
    OperatorParkExecutionResponseAction,
    check_operator_park_execution_response_action,
)
from ..models.operator_park_execution_response_phase import (
    OperatorParkExecutionResponsePhase,
    check_operator_park_execution_response_phase,
)
from ..models.operator_park_execution_response_terminal_state import (
    OperatorParkExecutionResponseTerminalState,
    check_operator_park_execution_response_terminal_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperatorParkExecutionResponse")


@_attrs_define
class OperatorParkExecutionResponse:
    """Verified force-park execution evidence for the original VM generation."""

    phase: OperatorParkExecutionResponsePhase
    node_id: str
    wake_id: str
    generation: str
    action: OperatorParkExecutionResponseAction
    capture_completed: bool
    """Capture and original VM cleanup were durably acknowledged."""
    cold_boot_required: bool
    """The verified result is stopped and requires a cold boot."""
    updated_at: datetime.datetime
    stopped_at: datetime.datetime | Unset = UNSET
    terminal_state: OperatorParkExecutionResponseTerminalState | Unset = UNSET
    reason: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        phase: str = self.phase

        node_id = self.node_id

        wake_id = self.wake_id

        generation = self.generation

        action: str = self.action

        capture_completed = self.capture_completed

        cold_boot_required = self.cold_boot_required

        updated_at = self.updated_at.isoformat()

        stopped_at: str | Unset = UNSET
        if not isinstance(self.stopped_at, Unset):
            stopped_at = self.stopped_at.isoformat()

        terminal_state: str | Unset = UNSET
        if not isinstance(self.terminal_state, Unset):
            terminal_state = self.terminal_state

        reason = self.reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "phase": phase,
                "node_id": node_id,
                "wake_id": wake_id,
                "generation": generation,
                "action": action,
                "capture_completed": capture_completed,
                "cold_boot_required": cold_boot_required,
                "updated_at": updated_at,
            }
        )
        if stopped_at is not UNSET:
            field_dict["stopped_at"] = stopped_at
        if terminal_state is not UNSET:
            field_dict["terminal_state"] = terminal_state
        if reason is not UNSET:
            field_dict["reason"] = reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        phase = check_operator_park_execution_response_phase(d.pop("phase"))

        node_id = d.pop("node_id")

        wake_id = d.pop("wake_id")

        generation = d.pop("generation")

        action = check_operator_park_execution_response_action(d.pop("action"))

        capture_completed = d.pop("capture_completed")

        cold_boot_required = d.pop("cold_boot_required")

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _stopped_at = d.pop("stopped_at", UNSET)
        stopped_at: datetime.datetime | Unset
        if isinstance(_stopped_at, Unset):
            stopped_at = UNSET
        else:
            stopped_at = datetime.datetime.fromisoformat(_stopped_at)

        _terminal_state = d.pop("terminal_state", UNSET)
        terminal_state: OperatorParkExecutionResponseTerminalState | Unset
        if isinstance(_terminal_state, Unset):
            terminal_state = UNSET
        else:
            terminal_state = check_operator_park_execution_response_terminal_state(_terminal_state)

        reason = d.pop("reason", UNSET)

        operator_park_execution_response = cls(
            phase=phase,
            node_id=node_id,
            wake_id=wake_id,
            generation=generation,
            action=action,
            capture_completed=capture_completed,
            cold_boot_required=cold_boot_required,
            updated_at=updated_at,
            stopped_at=stopped_at,
            terminal_state=terminal_state,
            reason=reason,
        )

        operator_park_execution_response.additional_properties = d
        return operator_park_execution_response

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
