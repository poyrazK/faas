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
    acknowledge_after_seconds: int | Unset = UNSET
    """OperationWorkflowPlaybookOutcomeDeadlinePolicy.acknowledge_after_seconds. Optional response window from
    actual assigned escalation delivery; zero disables monitoring."""
    fallback_owner: str | Unset = UNSET
    """OperationWorkflowPlaybookOutcomeDeadlinePolicy.fallback_owner. Optional distinct fallback team; requires a
    response window and never overrides manual assignments."""
    note: str | Unset = UNSET
    """OperationWorkflowPlaybookOutcomeDeadlinePolicy.note. Optional initial plain-text follow-up note; subsequent
    human notes are preserved."""

    def to_dict(self) -> dict[str, Any]:
        after_seconds = self.after_seconds

        owner = self.owner

        follow_up_after_seconds = self.follow_up_after_seconds

        acknowledge_after_seconds = self.acknowledge_after_seconds

        fallback_owner = self.fallback_owner

        note = self.note

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "after_seconds": after_seconds,
                "owner": owner,
                "follow_up_after_seconds": follow_up_after_seconds,
            }
        )
        if acknowledge_after_seconds is not UNSET:
            field_dict["acknowledge_after_seconds"] = acknowledge_after_seconds
        if fallback_owner is not UNSET:
            field_dict["fallback_owner"] = fallback_owner
        if note is not UNSET:
            field_dict["note"] = note

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        after_seconds = d.pop("after_seconds")

        owner = d.pop("owner")

        follow_up_after_seconds = d.pop("follow_up_after_seconds")

        acknowledge_after_seconds = d.pop("acknowledge_after_seconds", UNSET)

        fallback_owner = d.pop("fallback_owner", UNSET)

        note = d.pop("note", UNSET)

        operation_workflow_playbook_outcome_deadline_policy = cls(
            after_seconds=after_seconds,
            owner=owner,
            follow_up_after_seconds=follow_up_after_seconds,
            acknowledge_after_seconds=acknowledge_after_seconds,
            fallback_owner=fallback_owner,
            note=note,
        )

        return operation_workflow_playbook_outcome_deadline_policy
