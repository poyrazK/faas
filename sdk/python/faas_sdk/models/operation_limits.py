from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationLimits")


@_attrs_define
class OperationLimits:
    """ADR-1196 bounds fixed onto each accepted Operation. Every field is optional and bounded by the plan; workflow
    definitions take a deadline and an execution budget (ADR-20261016110803), and job definitions take none.

    """

    deadline_seconds: int | Unset = UNSET
    """Wall clock from acceptance, including waits, retries and recovery."""
    max_attempts: int | Unset = UNSET
    """Automatic attempts of one HTTP execution."""
    max_execution_seconds: int | Unset = UNSET
    """Runtime of HTTP attempts, or of a workflow's HTTP step attempts, summed across generations; waits do not
    count."""

    def to_dict(self) -> dict[str, Any]:
        deadline_seconds = self.deadline_seconds

        max_attempts = self.max_attempts

        max_execution_seconds = self.max_execution_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if deadline_seconds is not UNSET:
            field_dict["deadline_seconds"] = deadline_seconds
        if max_attempts is not UNSET:
            field_dict["max_attempts"] = max_attempts
        if max_execution_seconds is not UNSET:
            field_dict["max_execution_seconds"] = max_execution_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deadline_seconds = d.pop("deadline_seconds", UNSET)

        max_attempts = d.pop("max_attempts", UNSET)

        max_execution_seconds = d.pop("max_execution_seconds", UNSET)

        operation_limits = cls(
            deadline_seconds=deadline_seconds,
            max_attempts=max_attempts,
            max_execution_seconds=max_execution_seconds,
        )

        return operation_limits
