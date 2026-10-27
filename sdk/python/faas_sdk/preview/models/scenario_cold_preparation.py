from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scenario_cold_preparation_method import (
    ScenarioColdPreparationMethod,
    check_scenario_cold_preparation_method,
)
from ..models.scenario_cold_preparation_state import ScenarioColdPreparationState, check_scenario_cold_preparation_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="ScenarioColdPreparation")


@_attrs_define
class ScenarioColdPreparation:
    """Original-owner retirement and actual cold-boot receipts for one workload and deployment. Missing receipts never
    establish lifecycle parity.

    """

    id: UUID
    account_id: UUID
    app_id: UUID
    run_id: str
    workload: str
    deployment_id: UUID
    state: ScenarioColdPreparationState
    startup_ms: int
    instance_id: UUID | Unset = UNSET
    """Original serving VM when one existed at preparation."""
    recorder_session_id: UUID | Unset = UNSET
    """Original retired collector owner."""
    boot_instance_id: UUID | Unset = UNSET
    boot_recorder_session_id: UUID | Unset = UNSET
    wake_id: UUID | Unset = UNSET
    method: ScenarioColdPreparationMethod | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        account_id = str(self.account_id)

        app_id = str(self.app_id)

        run_id = self.run_id

        workload = self.workload

        deployment_id = str(self.deployment_id)

        state: str = self.state

        startup_ms = self.startup_ms

        instance_id: str | Unset = UNSET
        if not isinstance(self.instance_id, Unset):
            instance_id = str(self.instance_id)

        recorder_session_id: str | Unset = UNSET
        if not isinstance(self.recorder_session_id, Unset):
            recorder_session_id = str(self.recorder_session_id)

        boot_instance_id: str | Unset = UNSET
        if not isinstance(self.boot_instance_id, Unset):
            boot_instance_id = str(self.boot_instance_id)

        boot_recorder_session_id: str | Unset = UNSET
        if not isinstance(self.boot_recorder_session_id, Unset):
            boot_recorder_session_id = str(self.boot_recorder_session_id)

        wake_id: str | Unset = UNSET
        if not isinstance(self.wake_id, Unset):
            wake_id = str(self.wake_id)

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
                "state": state,
                "startup_ms": startup_ms,
            }
        )
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id
        if recorder_session_id is not UNSET:
            field_dict["recorder_session_id"] = recorder_session_id
        if boot_instance_id is not UNSET:
            field_dict["boot_instance_id"] = boot_instance_id
        if boot_recorder_session_id is not UNSET:
            field_dict["boot_recorder_session_id"] = boot_recorder_session_id
        if wake_id is not UNSET:
            field_dict["wake_id"] = wake_id
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

        state = check_scenario_cold_preparation_state(d.pop("state"))

        startup_ms = d.pop("startup_ms")

        _instance_id = d.pop("instance_id", UNSET)
        instance_id: UUID | Unset
        if isinstance(_instance_id, Unset):
            instance_id = UNSET
        else:
            instance_id = UUID(_instance_id)

        _recorder_session_id = d.pop("recorder_session_id", UNSET)
        recorder_session_id: UUID | Unset
        if isinstance(_recorder_session_id, Unset):
            recorder_session_id = UNSET
        else:
            recorder_session_id = UUID(_recorder_session_id)

        _boot_instance_id = d.pop("boot_instance_id", UNSET)
        boot_instance_id: UUID | Unset
        if isinstance(_boot_instance_id, Unset):
            boot_instance_id = UNSET
        else:
            boot_instance_id = UUID(_boot_instance_id)

        _boot_recorder_session_id = d.pop("boot_recorder_session_id", UNSET)
        boot_recorder_session_id: UUID | Unset
        if isinstance(_boot_recorder_session_id, Unset):
            boot_recorder_session_id = UNSET
        else:
            boot_recorder_session_id = UUID(_boot_recorder_session_id)

        _wake_id = d.pop("wake_id", UNSET)
        wake_id: UUID | Unset
        if isinstance(_wake_id, Unset):
            wake_id = UNSET
        else:
            wake_id = UUID(_wake_id)

        _method = d.pop("method", UNSET)
        method: ScenarioColdPreparationMethod | Unset
        if isinstance(_method, Unset):
            method = UNSET
        else:
            method = check_scenario_cold_preparation_method(_method)

        scenario_cold_preparation = cls(
            id=id,
            account_id=account_id,
            app_id=app_id,
            run_id=run_id,
            workload=workload,
            deployment_id=deployment_id,
            state=state,
            startup_ms=startup_ms,
            instance_id=instance_id,
            recorder_session_id=recorder_session_id,
            boot_instance_id=boot_instance_id,
            boot_recorder_session_id=boot_recorder_session_id,
            wake_id=wake_id,
            method=method,
        )

        scenario_cold_preparation.additional_properties = d
        return scenario_cold_preparation

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
