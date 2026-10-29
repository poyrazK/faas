from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_worker_fault_state import ScenarioWorkerFaultState, check_scenario_worker_fault_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="ScenarioWorkerFault")


@_attrs_define
class ScenarioWorkerFault:
    """Platform-owned checkpoint, collector and VM termination identity. Complete evidence requires a terminated receipt
    and independently journalled recovery on a different VM.

    """

    id: UUID
    account_id: UUID
    app_id: UUID
    run_id: str
    workload: str
    deployment_id: UUID
    instance_id: UUID
    invocation_id: UUID
    checkpoint_attempt_id: UUID
    recorder_session_id: UUID
    state: ScenarioWorkerFaultState
    recovery_instance_id: UUID | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        account_id = str(self.account_id)

        app_id = str(self.app_id)

        run_id = self.run_id

        workload = self.workload

        deployment_id = str(self.deployment_id)

        instance_id = str(self.instance_id)

        invocation_id = str(self.invocation_id)

        checkpoint_attempt_id = str(self.checkpoint_attempt_id)

        recorder_session_id = str(self.recorder_session_id)

        state: str = self.state

        recovery_instance_id: str | Unset = UNSET
        if not isinstance(self.recovery_instance_id, Unset):
            recovery_instance_id = str(self.recovery_instance_id)

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
                "invocation_id": invocation_id,
                "checkpoint_attempt_id": checkpoint_attempt_id,
                "recorder_session_id": recorder_session_id,
                "state": state,
            }
        )
        if recovery_instance_id is not UNSET:
            field_dict["recovery_instance_id"] = recovery_instance_id

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

        invocation_id = UUID(d.pop("invocation_id"))

        checkpoint_attempt_id = UUID(d.pop("checkpoint_attempt_id"))

        recorder_session_id = UUID(d.pop("recorder_session_id"))

        state = check_scenario_worker_fault_state(d.pop("state"))

        _recovery_instance_id = d.pop("recovery_instance_id", UNSET)
        recovery_instance_id: UUID | Unset
        if isinstance(_recovery_instance_id, Unset):
            recovery_instance_id = UNSET
        else:
            recovery_instance_id = UUID(_recovery_instance_id)

        scenario_worker_fault = cls(
            id=id,
            account_id=account_id,
            app_id=app_id,
            run_id=run_id,
            workload=workload,
            deployment_id=deployment_id,
            instance_id=instance_id,
            invocation_id=invocation_id,
            checkpoint_attempt_id=checkpoint_attempt_id,
            recorder_session_id=recorder_session_id,
            state=state,
            recovery_instance_id=recovery_instance_id,
        )

        scenario_worker_fault.additional_properties = d
        return scenario_worker_fault

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
