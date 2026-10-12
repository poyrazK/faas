from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowPlaybookOutcomeDeadlinePolicy")


@_attrs_define
class OperationWorkflowPlaybookOutcomeDeadlinePolicy:
    """OperationWorkflowPlaybookOutcomeDeadlinePolicy. Application-defined business attention policy. Requires an expected
    terminal outcome; all durations are whole seconds and the owner is a coordination label, not an external
    destination.

    """

    after_seconds: int
    """OperationWorkflowPlaybookOutcomeDeadlinePolicy.after_seconds. Expected wait from checklist creation, bounded
    to ten years."""
    owner: str
    """OperationWorkflowPlaybookOutcomeDeadlinePolicy.owner. Required responsible team label with no surrounding
    whitespace or control characters."""
    follow_up_after_seconds: int
    """OperationWorkflowPlaybookOutcomeDeadlinePolicy.follow_up_after_seconds. Assigned follow-up delay from actual
    notice delivery, independently bounded to one week."""
    note: str | Unset = UNSET
    """OperationWorkflowPlaybookOutcomeDeadlinePolicy.note. Optional initial plain-text follow-up note; subsequent
    human notes are preserved."""

    def to_dict(self) -> dict[str, Any]:
        after_seconds = self.after_seconds

        owner = self.owner

        follow_up_after_seconds = self.follow_up_after_seconds

        note = self.note

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "after_seconds": after_seconds,
                "owner": owner,
                "follow_up_after_seconds": follow_up_after_seconds,
            }
        )
        if note is not UNSET:
            field_dict["note"] = note

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        after_seconds = d.pop("after_seconds")

        owner = d.pop("owner")

        follow_up_after_seconds = d.pop("follow_up_after_seconds")

        note = d.pop("note", UNSET)

        operation_workflow_playbook_outcome_deadline_policy = cls(
            after_seconds=after_seconds,
            owner=owner,
            follow_up_after_seconds=follow_up_after_seconds,
            note=note,
        )

        return operation_workflow_playbook_outcome_deadline_policy
