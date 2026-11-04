from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.deployment_cron_activation_status import DeploymentCronActivationStatus


T = TypeVar("T", bound="DeploymentConfigurationStatus")


@_attrs_define
class DeploymentConfigurationStatus:
    """Server-managed cron activation diagnostics. CLI-owned settings are additionally reflected in CLI receipt
    configuration_complete. Reads never create builds or modify schedules.

    """

    complete: bool
    """True when no deferred cron activation is recorded or the tracked intent was activated."""
    crons: DeploymentCronActivationStatus
    """The latest retained cron intent for this deployment. Activated records historical completion even after a
    later deployment changes schedules."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        complete = self.complete

        crons = self.crons.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "complete": complete,
                "crons": crons,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.deployment_cron_activation_status import DeploymentCronActivationStatus

        d = dict(src_dict)
        complete = d.pop("complete")

        crons = DeploymentCronActivationStatus.from_dict(d.pop("crons"))

        deployment_configuration_status = cls(
            complete=complete,
            crons=crons,
        )

        deployment_configuration_status.additional_properties = d
        return deployment_configuration_status

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
