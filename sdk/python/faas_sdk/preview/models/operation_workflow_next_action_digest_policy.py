from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.operation_workflow_next_action_digest_policy_action_trigger import (
    OperationWorkflowNextActionDigestPolicyActionTrigger,
    check_operation_workflow_next_action_digest_policy_action_trigger,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowNextActionDigestPolicy")


@_attrs_define
class OperationWorkflowNextActionDigestPolicy:
    """Fixed team digest selection and cadence. Owner is both the exact action-owner filter and inbox recipient label, not
    proof of team membership. Overdue actions, deadlines within the configured approaching window and unaccepted
    handoffs qualify. Unchanged delivered items are suppressed.

    """

    owner: str
    """Nonblank trimmed control-free UTF-8 team label, at most 256 bytes."""
    workflow: str | Unset = UNSET
    """Optional exact workflow selector, independent of the subscription discovery workflow."""
    action_kind: str | Unset = UNSET
    """Optional exact next-action kind supported by the work queue."""
    action_trigger: OperationWorkflowNextActionDigestPolicyActionTrigger | Unset = UNSET
    cadence_seconds: int | Unset = 3600
    """Digest cadence, default one hour. The first evaluation is scheduled from subscription creation; retries
    retain the due time."""
    soon_after_seconds: int | Unset = 86400
    """Approaching deadline window from each successful evaluation, default one day. Undated suggestions do not
    qualify."""

    def to_dict(self) -> dict[str, Any]:
        owner = self.owner

        workflow = self.workflow

        action_kind = self.action_kind

        action_trigger: str | Unset = UNSET
        if not isinstance(self.action_trigger, Unset):
            action_trigger = self.action_trigger

        cadence_seconds = self.cadence_seconds

        soon_after_seconds = self.soon_after_seconds

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "owner": owner,
            }
        )
        if workflow is not UNSET:
            field_dict["workflow"] = workflow
        if action_kind is not UNSET:
            field_dict["action_kind"] = action_kind
        if action_trigger is not UNSET:
            field_dict["action_trigger"] = action_trigger
        if cadence_seconds is not UNSET:
            field_dict["cadence_seconds"] = cadence_seconds
        if soon_after_seconds is not UNSET:
            field_dict["soon_after_seconds"] = soon_after_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        owner = d.pop("owner")

        workflow = d.pop("workflow", UNSET)

        action_kind = d.pop("action_kind", UNSET)

        _action_trigger = d.pop("action_trigger", UNSET)
        action_trigger: OperationWorkflowNextActionDigestPolicyActionTrigger | Unset
        if isinstance(_action_trigger, Unset):
            action_trigger = UNSET
        else:
            action_trigger = check_operation_workflow_next_action_digest_policy_action_trigger(_action_trigger)

        cadence_seconds = d.pop("cadence_seconds", UNSET)

        soon_after_seconds = d.pop("soon_after_seconds", UNSET)

        operation_workflow_next_action_digest_policy = cls(
            owner=owner,
            workflow=workflow,
            action_kind=action_kind,
            action_trigger=action_trigger,
            cadence_seconds=cadence_seconds,
            soon_after_seconds=soon_after_seconds,
        )

        return operation_workflow_next_action_digest_policy
