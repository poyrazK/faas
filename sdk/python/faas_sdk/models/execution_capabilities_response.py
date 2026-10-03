from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.execution_capabilities_response_network_modes_item import (
    ExecutionCapabilitiesResponseNetworkModesItem,
    check_execution_capabilities_response_network_modes_item,
)
from ..models.execution_capabilities_response_runtimes_item import (
    ExecutionCapabilitiesResponseRuntimesItem,
    check_execution_capabilities_response_runtimes_item,
)
from ..models.execution_capabilities_response_unavailable_reasons_item import (
    ExecutionCapabilitiesResponseUnavailableReasonsItem,
    check_execution_capabilities_response_unavailable_reasons_item,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_capability_limits import ExecutionCapabilityLimits
    from ..models.execution_profile_capability import ExecutionProfileCapability


T = TypeVar("T", bound="ExecutionCapabilitiesResponse")


@_attrs_define
class ExecutionCapabilitiesResponse:
    """Supported Runs admission contract for the authenticated account.
    admission_available reports plan entitlement and the control-plane
    API gate only. It does not report scheduler dispatcher or profile image
    readiness.

    """

    plan: str
    """Account plan identifier."""
    admission_available: bool
    """True when the account is entitled and the control-plane admission gate is enabled."""
    plan_entitled: bool
    """Whether the account plan allows Runs."""
    control_plane_enabled: bool
    """Whether the apid Runs admission gate is enabled."""
    runtimes: list[ExecutionCapabilitiesResponseRuntimesItem]
    profiles: list[ExecutionProfileCapability]
    network_modes: list[ExecutionCapabilitiesResponseNetworkModesItem]
    unavailable_reasons: list[ExecutionCapabilitiesResponseUnavailableReasonsItem] | Unset = UNSET
    limits: ExecutionCapabilityLimits | Unset = UNSET
    """Plan limits and fixed request-shape caps for Runs."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        plan = self.plan

        admission_available = self.admission_available

        plan_entitled = self.plan_entitled

        control_plane_enabled = self.control_plane_enabled

        runtimes = []
        for runtimes_item_data in self.runtimes:
            runtimes_item: str = runtimes_item_data
            runtimes.append(runtimes_item)

        profiles = []
        for profiles_item_data in self.profiles:
            profiles_item = profiles_item_data.to_dict()
            profiles.append(profiles_item)

        network_modes = []
        for network_modes_item_data in self.network_modes:
            network_modes_item: str = network_modes_item_data
            network_modes.append(network_modes_item)

        unavailable_reasons: list[str] | Unset = UNSET
        if not isinstance(self.unavailable_reasons, Unset):
            unavailable_reasons = []
            for unavailable_reasons_item_data in self.unavailable_reasons:
                unavailable_reasons_item: str = unavailable_reasons_item_data
                unavailable_reasons.append(unavailable_reasons_item)

        limits: dict[str, Any] | Unset = UNSET
        if not isinstance(self.limits, Unset):
            limits = self.limits.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "plan": plan,
                "admission_available": admission_available,
                "plan_entitled": plan_entitled,
                "control_plane_enabled": control_plane_enabled,
                "runtimes": runtimes,
                "profiles": profiles,
                "network_modes": network_modes,
            }
        )
        if unavailable_reasons is not UNSET:
            field_dict["unavailable_reasons"] = unavailable_reasons
        if limits is not UNSET:
            field_dict["limits"] = limits

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_capability_limits import ExecutionCapabilityLimits
        from ..models.execution_profile_capability import ExecutionProfileCapability

        d = dict(src_dict)
        plan = d.pop("plan")

        admission_available = d.pop("admission_available")

        plan_entitled = d.pop("plan_entitled")

        control_plane_enabled = d.pop("control_plane_enabled")

        runtimes = []
        _runtimes = d.pop("runtimes")
        for runtimes_item_data in _runtimes:
            runtimes_item = check_execution_capabilities_response_runtimes_item(runtimes_item_data)

            runtimes.append(runtimes_item)

        profiles = []
        _profiles = d.pop("profiles")
        for profiles_item_data in _profiles:
            profiles_item = ExecutionProfileCapability.from_dict(profiles_item_data)

            profiles.append(profiles_item)

        network_modes = []
        _network_modes = d.pop("network_modes")
        for network_modes_item_data in _network_modes:
            network_modes_item = check_execution_capabilities_response_network_modes_item(network_modes_item_data)

            network_modes.append(network_modes_item)

        _unavailable_reasons = d.pop("unavailable_reasons", UNSET)
        unavailable_reasons: list[ExecutionCapabilitiesResponseUnavailableReasonsItem] | Unset = UNSET
        if _unavailable_reasons is not UNSET:
            unavailable_reasons = []
            for unavailable_reasons_item_data in _unavailable_reasons:
                unavailable_reasons_item = check_execution_capabilities_response_unavailable_reasons_item(
                    unavailable_reasons_item_data
                )

                unavailable_reasons.append(unavailable_reasons_item)

        _limits = d.pop("limits", UNSET)
        limits: ExecutionCapabilityLimits | Unset
        if isinstance(_limits, Unset):
            limits = UNSET
        else:
            limits = ExecutionCapabilityLimits.from_dict(_limits)

        execution_capabilities_response = cls(
            plan=plan,
            admission_available=admission_available,
            plan_entitled=plan_entitled,
            control_plane_enabled=control_plane_enabled,
            runtimes=runtimes,
            profiles=profiles,
            network_modes=network_modes,
            unavailable_reasons=unavailable_reasons,
            limits=limits,
        )

        execution_capabilities_response.additional_properties = d
        return execution_capabilities_response

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
