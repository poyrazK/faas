from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_initial_evidence_diagnosis import WorkflowInitialEvidenceDiagnosis
    from ..models.workflow_recovery_automatic_blocked_notification import WorkflowRecoveryAutomaticBlockedNotification


T = TypeVar("T", bound="WorkflowInitialRecoveryAttention")


@_attrs_define
class WorkflowInitialRecoveryAttention:
    """Latest committed observation of a submitted manual initial recovery. Reads do not update it. Notification receipts
    describe captured historical transitions.

    """

    reason: str
    sequence: int
    observed_at: datetime.datetime
    block_reason: str | Unset = UNSET
    diagnosis: WorkflowInitialEvidenceDiagnosis | Unset = UNSET
    """Consistent diagnosis of retained evidence for an initial recovery review."""
    operator_action: str | Unset = UNSET
    evidence_url: str | Unset = UNSET
    cli_command: str | Unset = UNSET
    notification: WorkflowRecoveryAutomaticBlockedNotification | Unset = UNSET
    """Latest blocked or recovered evidence capture for a retry or initial-recovery review. Retry sequence
    increases only on a changed evidence diagnosis or proven clearance. Receivers are frozen at capture; queued
    identifies ledger admission rather than successful delivery. No receivers prevents historical replay to later
    subscribers."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        reason = self.reason

        sequence = self.sequence

        observed_at = self.observed_at.isoformat()

        block_reason = self.block_reason

        diagnosis: dict[str, Any] | Unset = UNSET
        if not isinstance(self.diagnosis, Unset):
            diagnosis = self.diagnosis.to_dict()

        operator_action = self.operator_action

        evidence_url = self.evidence_url

        cli_command = self.cli_command

        notification: dict[str, Any] | Unset = UNSET
        if not isinstance(self.notification, Unset):
            notification = self.notification.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "reason": reason,
                "sequence": sequence,
                "observed_at": observed_at,
            }
        )
        if block_reason is not UNSET:
            field_dict["block_reason"] = block_reason
        if diagnosis is not UNSET:
            field_dict["diagnosis"] = diagnosis
        if operator_action is not UNSET:
            field_dict["operator_action"] = operator_action
        if evidence_url is not UNSET:
            field_dict["evidence_url"] = evidence_url
        if cli_command is not UNSET:
            field_dict["cli_command"] = cli_command
        if notification is not UNSET:
            field_dict["notification"] = notification

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_initial_evidence_diagnosis import WorkflowInitialEvidenceDiagnosis
        from ..models.workflow_recovery_automatic_blocked_notification import (
            WorkflowRecoveryAutomaticBlockedNotification,
        )

        d = dict(src_dict)
        reason = d.pop("reason")

        sequence = d.pop("sequence")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        block_reason = d.pop("block_reason", UNSET)

        _diagnosis = d.pop("diagnosis", UNSET)
        diagnosis: WorkflowInitialEvidenceDiagnosis | Unset
        if isinstance(_diagnosis, Unset):
            diagnosis = UNSET
        else:
            diagnosis = WorkflowInitialEvidenceDiagnosis.from_dict(_diagnosis)

        operator_action = d.pop("operator_action", UNSET)

        evidence_url = d.pop("evidence_url", UNSET)

        cli_command = d.pop("cli_command", UNSET)

        _notification = d.pop("notification", UNSET)
        notification: WorkflowRecoveryAutomaticBlockedNotification | Unset
        if isinstance(_notification, Unset):
            notification = UNSET
        else:
            notification = WorkflowRecoveryAutomaticBlockedNotification.from_dict(_notification)

        workflow_initial_recovery_attention = cls(
            reason=reason,
            sequence=sequence,
            observed_at=observed_at,
            block_reason=block_reason,
            diagnosis=diagnosis,
            operator_action=operator_action,
            evidence_url=evidence_url,
            cli_command=cli_command,
            notification=notification,
        )

        workflow_initial_recovery_attention.additional_properties = d
        return workflow_initial_recovery_attention

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
