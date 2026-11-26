from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.data_api_inspection_response_task_status import (
    DataAPIInspectionResponseTaskStatus,
    check_data_api_inspection_response_task_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_api_inspection_response_binding_identity import DataAPIInspectionResponseBindingIdentity
    from ..models.data_api_inspection_response_observation import DataAPIInspectionResponseObservation


T = TypeVar("T", bound="DataAPIInspectionResponse")


@_attrs_define
class DataAPIInspectionResponse:
    """Reports the status and verified identity of a Data API inspection."""

    task_id: UUID
    app_id: UUID
    deployment_id: UUID
    database_id: UUID
    binding_id: UUID
    credential_generation: int
    task_status: DataAPIInspectionResponseTaskStatus
    binding_identity: DataAPIInspectionResponseBindingIdentity
    observation: DataAPIInspectionResponseObservation | Unset = UNSET
    """Bounded catalog metadata, present only when execution and TLS trust were verified."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        task_id = str(self.task_id)

        app_id = str(self.app_id)

        deployment_id = str(self.deployment_id)

        database_id = str(self.database_id)

        binding_id = str(self.binding_id)

        credential_generation = self.credential_generation

        task_status: str = self.task_status

        binding_identity = self.binding_identity.to_dict()

        observation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.observation, Unset):
            observation = self.observation.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "task_id": task_id,
                "app_id": app_id,
                "deployment_id": deployment_id,
                "database_id": database_id,
                "binding_id": binding_id,
                "credential_generation": credential_generation,
                "task_status": task_status,
                "binding_identity": binding_identity,
            }
        )
        if observation is not UNSET:
            field_dict["observation"] = observation

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_api_inspection_response_binding_identity import DataAPIInspectionResponseBindingIdentity
        from ..models.data_api_inspection_response_observation import DataAPIInspectionResponseObservation

        d = dict(src_dict)
        task_id = UUID(d.pop("task_id"))

        app_id = UUID(d.pop("app_id"))

        deployment_id = UUID(d.pop("deployment_id"))

        database_id = UUID(d.pop("database_id"))

        binding_id = UUID(d.pop("binding_id"))

        credential_generation = d.pop("credential_generation")

        task_status = check_data_api_inspection_response_task_status(d.pop("task_status"))

        binding_identity = DataAPIInspectionResponseBindingIdentity.from_dict(d.pop("binding_identity"))

        _observation = d.pop("observation", UNSET)
        observation: DataAPIInspectionResponseObservation | Unset
        if isinstance(_observation, Unset):
            observation = UNSET
        else:
            observation = DataAPIInspectionResponseObservation.from_dict(_observation)

        data_api_inspection_response = cls(
            task_id=task_id,
            app_id=app_id,
            deployment_id=deployment_id,
            database_id=database_id,
            binding_id=binding_id,
            credential_generation=credential_generation,
            task_status=task_status,
            binding_identity=binding_identity,
            observation=observation,
        )

        data_api_inspection_response.additional_properties = d
        return data_api_inspection_response

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
