from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_environment_queue_bindings_response_activation_state import (
    ProjectEnvironmentQueueBindingsResponseActivationState,
    check_project_environment_queue_bindings_response_activation_state,
)

if TYPE_CHECKING:
    from ..models.project_environment_queue_binding import ProjectEnvironmentQueueBinding


T = TypeVar("T", bound="ProjectEnvironmentQueueBindingsResponse")


@_attrs_define
class ProjectEnvironmentQueueBindingsResponse:
    """Desired stage queue definitions. Consumer activation is currently unavailable and these definitions do not enable
    delivery or qualify promotion.

    """

    environment: str
    workload: str
    revision: int
    """Queue collection clock."""
    workload_revision: int
    """Complete workload revision to use as expected_revision on replacement."""
    config_hash: str
    activation_state: ProjectEnvironmentQueueBindingsResponseActivationState
    bindings: list[ProjectEnvironmentQueueBinding]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        environment = self.environment

        workload = self.workload

        revision = self.revision

        workload_revision = self.workload_revision

        config_hash = self.config_hash

        activation_state: str = self.activation_state

        bindings = []
        for bindings_item_data in self.bindings:
            bindings_item = bindings_item_data.to_dict()
            bindings.append(bindings_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "environment": environment,
                "workload": workload,
                "revision": revision,
                "workload_revision": workload_revision,
                "config_hash": config_hash,
                "activation_state": activation_state,
                "bindings": bindings,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_environment_queue_binding import ProjectEnvironmentQueueBinding

        d = dict(src_dict)
        environment = d.pop("environment")

        workload = d.pop("workload")

        revision = d.pop("revision")

        workload_revision = d.pop("workload_revision")

        config_hash = d.pop("config_hash")

        activation_state = check_project_environment_queue_bindings_response_activation_state(d.pop("activation_state"))

        bindings = []
        _bindings = d.pop("bindings")
        for bindings_item_data in _bindings:
            bindings_item = ProjectEnvironmentQueueBinding.from_dict(bindings_item_data)

            bindings.append(bindings_item)

        project_environment_queue_bindings_response = cls(
            environment=environment,
            workload=workload,
            revision=revision,
            workload_revision=workload_revision,
            config_hash=config_hash,
            activation_state=activation_state,
            bindings=bindings,
        )

        project_environment_queue_bindings_response.additional_properties = d
        return project_environment_queue_bindings_response

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
