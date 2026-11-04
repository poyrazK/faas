from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_suspend_resume_receipt_method import (
    ScenarioSuspendResumeReceiptMethod,
    check_scenario_suspend_resume_receipt_method,
)
from ..models.scenario_suspend_resume_receipt_state import (
    ScenarioSuspendResumeReceiptState,
    check_scenario_suspend_resume_receipt_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ScenarioSuspendResumeReceipt")


@_attrs_define
class ScenarioSuspendResumeReceipt:
    """Original-owner physical pause and resume proof, acknowledged by the scheduler after publishing the same VM with its
    captured runtime configuration.

    """

    id: UUID
    account_id: UUID
    app_id: UUID
    run_id: str
    workload: str
    deployment_id: UUID
    instance_id: UUID
    recorder_session_id: UUID
    state: ScenarioSuspendResumeReceiptState
    pause_ms: int
    resume_ms: int
    invocation_id: UUID | Unset = UNSET
    """Enrolled managed job; present only for managed_job checkpoints."""
    invocation_attempt: int | Unset = UNSET
    """First dispatch held across the pause."""
    checkpoint_attempt_id: UUID | Unset = UNSET
    """Independently completed simulation held by the original relay."""
    method: ScenarioSuspendResumeReceiptMethod | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        account_id = str(self.account_id)

        app_id = str(self.app_id)

        run_id = self.run_id

        workload = self.workload

        deployment_id = str(self.deployment_id)

        instance_id = str(self.instance_id)

        recorder_session_id = str(self.recorder_session_id)

        state: str = self.state

        pause_ms = self.pause_ms

        resume_ms = self.resume_ms

        invocation_id: str | Unset = UNSET
        if not isinstance(self.invocation_id, Unset):
            invocation_id = str(self.invocation_id)

        invocation_attempt = self.invocation_attempt

        checkpoint_attempt_id: str | Unset = UNSET
        if not isinstance(self.checkpoint_attempt_id, Unset):
            checkpoint_attempt_id = str(self.checkpoint_attempt_id)

        method: str | Unset = UNSET
        if not isinstance(self.method, Unset):
            method = self.method

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "account_id": account_id,
                "app_id": app_id,
                "run_id": run_id,
                "workload": workload,
                "deployment_id": deployment_id,
                "instance_id": instance_id,
                "recorder_session_id": recorder_session_id,
                "state": state,
                "pause_ms": pause_ms,
                "resume_ms": resume_ms,
            }
        )
        if invocation_id is not UNSET:
            field_dict["invocation_id"] = invocation_id
        if invocation_attempt is not UNSET:
            field_dict["invocation_attempt"] = invocation_attempt
        if checkpoint_attempt_id is not UNSET:
            field_dict["checkpoint_attempt_id"] = checkpoint_attempt_id
        if method is not UNSET:
            field_dict["method"] = method

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        account_id = UUID(d.pop("account_id"))

        app_id = UUID(d.pop("app_id"))

        run_id = d.pop("run_id")

        workload = d.pop("workload")

        deployment_id = UUID(d.pop("deployment_id"))

        instance_id = UUID(d.pop("instance_id"))

        recorder_session_id = UUID(d.pop("recorder_session_id"))

        state = check_scenario_suspend_resume_receipt_state(d.pop("state"))

        pause_ms = d.pop("pause_ms")

        resume_ms = d.pop("resume_ms")

        _invocation_id = d.pop("invocation_id", UNSET)
        invocation_id: UUID | Unset
        if isinstance(_invocation_id, Unset):
            invocation_id = UNSET
        else:
            invocation_id = UUID(_invocation_id)

        invocation_attempt = d.pop("invocation_attempt", UNSET)

        _checkpoint_attempt_id = d.pop("checkpoint_attempt_id", UNSET)
        checkpoint_attempt_id: UUID | Unset
        if isinstance(_checkpoint_attempt_id, Unset):
            checkpoint_attempt_id = UNSET
        else:
            checkpoint_attempt_id = UUID(_checkpoint_attempt_id)

        _method = d.pop("method", UNSET)
        method: ScenarioSuspendResumeReceiptMethod | Unset
        if isinstance(_method, Unset):
            method = UNSET
        else:
            method = check_scenario_suspend_resume_receipt_method(_method)

        scenario_suspend_resume_receipt = cls(
            id=id,
            account_id=account_id,
            app_id=app_id,
            run_id=run_id,
            workload=workload,
            deployment_id=deployment_id,
            instance_id=instance_id,
            recorder_session_id=recorder_session_id,
            state=state,
            pause_ms=pause_ms,
            resume_ms=resume_ms,
            invocation_id=invocation_id,
            invocation_attempt=invocation_attempt,
            checkpoint_attempt_id=checkpoint_attempt_id,
            method=method,
        )

        scenario_suspend_resume_receipt.additional_properties = d
        return scenario_suspend_resume_receipt

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
