from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.deployment_build_dependency_gate_status import (
    DeploymentBuildDependencyGateStatus,
    check_deployment_build_dependency_gate_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.deployment_dependency_pin import DeploymentDependencyPin


T = TypeVar("T", bound="DeploymentBuildDependencyGate")


@_attrs_define
class DeploymentBuildDependencyGate:
    """Build-time wait on the API deployments a guided service migration selected (ADR-1666). It is checked before cache
    lookup and slot allocation and never holds a build slot while waiting.

    """

    dependencies: list[DeploymentDependencyPin]
    status: DeploymentBuildDependencyGateStatus
    deadline_at: datetime.datetime
    check_after: datetime.datetime
    """Earliest time the gate is re-checked. Zero time (0001-01-01T00:00:00Z) when no re-check is scheduled."""
    code: str | Unset = UNSET
    """Machine-readable reason while waiting or failed, for example build_dependency_not_ready or
    build_dependency_timeout. Absent once ready."""
    blocker: str | Unset = UNSET
    """The dependency or deadline currently blocking the build."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        dependencies = []
        for dependencies_item_data in self.dependencies:
            dependencies_item = dependencies_item_data.to_dict()
            dependencies.append(dependencies_item)

        status: str = self.status

        deadline_at = self.deadline_at.isoformat()

        check_after = self.check_after.isoformat()

        code = self.code

        blocker = self.blocker

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "dependencies": dependencies,
                "status": status,
                "deadline_at": deadline_at,
                "check_after": check_after,
            }
        )
        if code is not UNSET:
            field_dict["code"] = code
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

        status = check_deployment_build_dependency_gate_status(d.pop("status"))

        deadline_at = datetime.datetime.fromisoformat(d.pop("deadline_at"))

        check_after = datetime.datetime.fromisoformat(d.pop("check_after"))

        code = d.pop("code", UNSET)

        blocker = d.pop("blocker", UNSET)

        deployment_build_dependency_gate = cls(
            dependencies=dependencies,
            status=status,
            deadline_at=deadline_at,
            check_after=check_after,
            code=code,
            blocker=blocker,
        )

        deployment_build_dependency_gate.additional_properties = d
        return deployment_build_dependency_gate

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
