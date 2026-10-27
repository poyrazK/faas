from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowBlockerEscalationPolicy")


@_attrs_define
class OperationWorkflowBlockerEscalationPolicy:
    """Application-declared recommendation to escalate a blocker once its known age reaches a threshold. Does not assign
    work or execute actions.

    """

    after_seconds: int
    """Whole seconds from first_observed_at, bounded to ten years."""
    owner: str
    """Public recommended escalation team or person identifier, limited to 128 UTF-8 bytes without control
    characters."""

    def to_dict(self) -> dict[str, Any]:
        after_seconds = self.after_seconds

        owner = self.owner

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "after_seconds": after_seconds,
                "owner": owner,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        after_seconds = d.pop("after_seconds")

        owner = d.pop("owner")

        operation_workflow_blocker_escalation_policy = cls(
            after_seconds=after_seconds,
            owner=owner,
        )

        return operation_workflow_blocker_escalation_policy
