from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.debug_guest_execution_evidence_error_class import (
    DebugGuestExecutionEvidenceErrorClass,
    check_debug_guest_execution_evidence_error_class,
)
from ..models.debug_guest_execution_evidence_outcome import (
    DebugGuestExecutionEvidenceOutcome,
    check_debug_guest_execution_evidence_outcome,
)
from ..models.debug_guest_execution_evidence_runtime import (
    DebugGuestExecutionEvidenceRuntime,
    check_debug_guest_execution_evidence_runtime,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DebugGuestExecutionEvidence")


@_attrs_define
class DebugGuestExecutionEvidence:
    """Bounded, platform-owned runtime execution evidence. Omitted when the runner signal was unavailable."""

    runtime: DebugGuestExecutionEvidenceRuntime
    duration_ms: int
    outcome: DebugGuestExecutionEvidenceOutcome
    error_class: DebugGuestExecutionEvidenceErrorClass | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        runtime: str = self.runtime

        duration_ms = self.duration_ms

        outcome: str = self.outcome

        error_class: str | Unset = UNSET
        if not isinstance(self.error_class, Unset):
            error_class = self.error_class

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "runtime": runtime,
                "duration_ms": duration_ms,
                "outcome": outcome,
            }
        )
        if error_class is not UNSET:
            field_dict["error_class"] = error_class

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        runtime = check_debug_guest_execution_evidence_runtime(d.pop("runtime"))

        duration_ms = d.pop("duration_ms")

        outcome = check_debug_guest_execution_evidence_outcome(d.pop("outcome"))

        _error_class = d.pop("error_class", UNSET)
        error_class: DebugGuestExecutionEvidenceErrorClass | Unset
        if isinstance(_error_class, Unset):
            error_class = UNSET
        else:
            error_class = check_debug_guest_execution_evidence_error_class(_error_class)

        debug_guest_execution_evidence = cls(
            runtime=runtime,
            duration_ms=duration_ms,
            outcome=outcome,
            error_class=error_class,
        )

        debug_guest_execution_evidence.additional_properties = d
        return debug_guest_execution_evidence

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
