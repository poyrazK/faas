from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.binding_runtime_deployment_status import (
    BindingRuntimeDeploymentStatus,
    check_binding_runtime_deployment_status,
)

if TYPE_CHECKING:
    from ..models.binding_runtime_instance_counts import BindingRuntimeInstanceCounts


T = TypeVar("T", bound="BindingRuntimeDeployment")


@_attrs_define
class BindingRuntimeDeployment:
    """A live deployment or one retaining resident app instances. Serving counts running rows; resident includes serving,
    starting, warm, snapshotting, draining and migrating rows. Parked and terminal rows are excluded.

    """

    deployment_id: UUID
    scope: str
    deployment_status: str
    status: BindingRuntimeDeploymentStatus
    """Stale residents take precedence, then unknown residents, then starting instances. Current does not imply
    connectivity or readiness."""
    serving: BindingRuntimeInstanceCounts
    """Disjoint counts compared with the app-wide change stamp. Current means admitted after the stamp; stale means
    admitted at or before it; unknown means missing stamp or start timestamp."""
    resident: BindingRuntimeInstanceCounts
    """Disjoint counts compared with the app-wide change stamp. Current means admitted after the stamp; stale means
    admitted at or before it; unknown means missing stamp or start timestamp."""
    starting: int
    """Waking or cold-booting instances, also included in resident counts."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        scope = self.scope

        deployment_status = self.deployment_status

        status: str = self.status

        serving = self.serving.to_dict()

        resident = self.resident.to_dict()

        starting = self.starting

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "scope": scope,
                "deployment_status": deployment_status,
                "status": status,
                "serving": serving,
                "resident": resident,
                "starting": starting,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_runtime_instance_counts import BindingRuntimeInstanceCounts

        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        scope = d.pop("scope")

        deployment_status = d.pop("deployment_status")

        status = check_binding_runtime_deployment_status(d.pop("status"))

        serving = BindingRuntimeInstanceCounts.from_dict(d.pop("serving"))

        resident = BindingRuntimeInstanceCounts.from_dict(d.pop("resident"))

        starting = d.pop("starting")

        binding_runtime_deployment = cls(
            deployment_id=deployment_id,
            scope=scope,
            deployment_status=deployment_status,
            status=status,
            serving=serving,
            resident=resident,
            starting=starting,
        )

        binding_runtime_deployment.additional_properties = d
        return binding_runtime_deployment

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
