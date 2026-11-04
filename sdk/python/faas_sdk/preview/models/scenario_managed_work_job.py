from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.scenario_managed_work_attempt import ScenarioManagedWorkAttempt


T = TypeVar("T", bound="ScenarioManagedWorkJob")


@_attrs_define
class ScenarioManagedWorkJob:
    """One managed async invocation. Runtime identities support inspection; comparison retains request digest and retry
    outcomes.

    """

    id: UUID
    workload: str
    app_id: UUID
    deployment_id: str
    method: str
    path: str
    payload_sha256: str
    state: str
    attempts: list[ScenarioManagedWorkAttempt]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        workload = self.workload

        app_id = str(self.app_id)

        deployment_id = self.deployment_id

        method = self.method

        path = self.path

        payload_sha256 = self.payload_sha256

        state = self.state

        attempts = []
        for attempts_item_data in self.attempts:
            attempts_item = attempts_item_data.to_dict()
            attempts.append(attempts_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "workload": workload,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "method": method,
                "path": path,
                "payload_sha256": payload_sha256,
                "state": state,
                "attempts": attempts,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_managed_work_attempt import ScenarioManagedWorkAttempt

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        workload = d.pop("workload")

        app_id = UUID(d.pop("app_id"))

        deployment_id = d.pop("deployment_id")

        method = d.pop("method")

        path = d.pop("path")

        payload_sha256 = d.pop("payload_sha256")

        state = d.pop("state")

        attempts = []
        _attempts = d.pop("attempts")
        for attempts_item_data in _attempts:
            attempts_item = ScenarioManagedWorkAttempt.from_dict(attempts_item_data)

            attempts.append(attempts_item)

        scenario_managed_work_job = cls(
            id=id,
            workload=workload,
            app_id=app_id,
            deployment_id=deployment_id,
            method=method,
            path=path,
            payload_sha256=payload_sha256,
            state=state,
            attempts=attempts,
        )

        scenario_managed_work_job.additional_properties = d
        return scenario_managed_work_job

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
