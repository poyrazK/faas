from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowPlaybookOutcomeGuidance")


@_attrs_define
class OperationWorkflowPlaybookOutcomeGuidance:
    """OperationWorkflowPlaybookOutcomeGuidance. Application-defined business meaning and optional suggested human follow-
    up, frozen with the playbook version. Plain text only; never establishes execution authority or changes outcome
    acceptance.

    """

    outcome_code: str
    """OperationWorkflowPlaybookOutcomeGuidance.outcome_code. Primary expected outcome or explicit acceptable
    alternative; unique within the playbook guidance set."""
    description: str
    """OperationWorkflowPlaybookOutcomeGuidance.description. Required trimmed UTF-8 plain-text business meaning, at
    most 1024 bytes and no control characters."""
    suggested_follow_up: str | Unset = UNSET
    """OperationWorkflowPlaybookOutcomeGuidance.suggested_follow_up. Optional trimmed UTF-8 plain-text suggestion,
    at most 1024 bytes and no control characters; application-owned decisions and execution remain separate."""

    def to_dict(self) -> dict[str, Any]:
        outcome_code = self.outcome_code

        description = self.description

        suggested_follow_up = self.suggested_follow_up

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "outcome_code": outcome_code,
                "description": description,
            }
        )
        if suggested_follow_up is not UNSET:
            field_dict["suggested_follow_up"] = suggested_follow_up

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        outcome_code = d.pop("outcome_code")

        description = d.pop("description")

        suggested_follow_up = d.pop("suggested_follow_up", UNSET)

        operation_workflow_playbook_outcome_guidance = cls(
            outcome_code=outcome_code,
            description=description,
            suggested_follow_up=suggested_follow_up,
        )

        return operation_workflow_playbook_outcome_guidance
