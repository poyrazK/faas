from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.deployment_dependency_gate_status import (
    DeploymentDependencyGateStatus,
    check_deployment_dependency_gate_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.deployment_dependency_pin import DeploymentDependencyPin


T = TypeVar("T", bound="DeploymentDependencyGate")


@_attrs_define
class DeploymentDependencyGate:
    """Compose dependency readiness gate pinned at admission. The deadline starts when the candidate first presents its own
    readiness proof.

    """

    dependencies: list[DeploymentDependencyPin]
    status: DeploymentDependencyGateStatus
    started_at: datetime.datetime | Unset = UNSET
    deadline_at: datetime.datetime | Unset = UNSET
    blocker: str | Unset = UNSET
    """The dependency or deadline currently blocking release."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        dependencies = []
        for dependencies_item_data in self.dependencies:
            dependencies_item = dependencies_item_data.to_dict()
            dependencies.append(dependencies_item)

        status: str = self.status

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        deadline_at: str | Unset = UNSET
        if not isinstance(self.deadline_at, Unset):
            deadline_at = self.deadline_at.isoformat()

        blocker = self.blocker

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "dependencies": dependencies,
                "status": status,
            }
        )
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if deadline_at is not UNSET:
            field_dict["deadline_at"] = deadline_at
        if blocker is not UNSET:
            field_dict["blocker"] = blocker

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.deployment_dependency_pin import DeploymentDependencyPin

        d = dict(src_dict)
        dependencies = []
        _dependencies = d.pop("dependencies")
        for dependencies_item_data in _dependencies:
            dependencies_item = DeploymentDependencyPin.from_dict(dependencies_item_data)

            dependencies.append(dependencies_item)

        status = check_deployment_dependency_gate_status(d.pop("status"))

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _deadline_at = d.pop("deadline_at", UNSET)
        deadline_at: datetime.datetime | Unset
        if isinstance(_deadline_at, Unset):
            deadline_at = UNSET
        else:
            deadline_at = datetime.datetime.fromisoformat(_deadline_at)

        blocker = d.pop("blocker", UNSET)

        deployment_dependency_gate = cls(
            dependencies=dependencies,
            status=status,
            started_at=started_at,
            deadline_at=deadline_at,
            blocker=blocker,
        )

        deployment_dependency_gate.additional_properties = d
        return deployment_dependency_gate

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
