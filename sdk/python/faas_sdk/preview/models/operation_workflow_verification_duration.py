from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowVerificationDuration")


@_attrs_define
class OperationWorkflowVerificationDuration:
    """Publication-based proof waiting time and coverage counts for one verification owner within an instance."""

    owner: str
    """Application-assigned verification owner. Empty means unassigned."""
    observed_seconds: int
    resolution_count: int
    pending_count: int
    unknown_start_count: int
    """Retained obligations whose start report is outside the history window. No wait duration is inferred for
    these obligations."""

    def to_dict(self) -> dict[str, Any]:
        owner = self.owner

        observed_seconds = self.observed_seconds

        resolution_count = self.resolution_count

        pending_count = self.pending_count

        unknown_start_count = self.unknown_start_count

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "owner": owner,
                "observed_seconds": observed_seconds,
                "resolution_count": resolution_count,
                "pending_count": pending_count,
                "unknown_start_count": unknown_start_count,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        owner = d.pop("owner")

        observed_seconds = d.pop("observed_seconds")

        resolution_count = d.pop("resolution_count")

        pending_count = d.pop("pending_count")

        unknown_start_count = d.pop("unknown_start_count")

        operation_workflow_verification_duration = cls(
            owner=owner,
            observed_seconds=observed_seconds,
            resolution_count=resolution_count,
            pending_count=pending_count,
            unknown_start_count=unknown_start_count,
        )

        return operation_workflow_verification_duration
