from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.execution_capabilities_response_environment_runtimes_item import (
    ExecutionCapabilitiesResponseEnvironmentRuntimesItem,
    check_execution_capabilities_response_environment_runtimes_item,
)
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
    from ..models.execution_class_capability import ExecutionClassCapability
    from ..models.execution_environment_limits import ExecutionEnvironmentLimits
    from ..models.execution_file_limits import ExecutionFileLimits
    from ..models.execution_profile_capability import ExecutionProfileCapability
    from ..models.run_control_capabilities import RunControlCapabilities


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
    """Whether the API Runs admission gate is enabled."""
    runtimes: list[ExecutionCapabilitiesResponseRuntimesItem]
    profiles: list[ExecutionProfileCapability]
    network_modes: list[ExecutionCapabilitiesResponseNetworkModesItem]
    artifact_deliveries: bool | Unset = UNSET
    """New Run artifact deliveries are currently admitted into configured app upload routes."""
    task_pipelines: bool | Unset = UNSET
    """Whether new version-pinned task pipelines and batches are enabled."""
    run_controls: RunControlCapabilities | Unset = UNSET
    """Available budget, attribution and history controls for this account."""
    custom_environments: bool | Unset = UNSET
    environment_runtimes: list[ExecutionCapabilitiesResponseEnvironmentRuntimesItem] | Unset = UNSET
    environment_limits: ExecutionEnvironmentLimits | Unset = UNSET
    """Dependency recipe and image limits for the current account."""
    execution_classes: list[ExecutionClassCapability] | Unset = UNSET
    extended_admissions: bool | Unset = UNSET
    """New standalone contract and stored-file admissions are enabled."""
    file_limits: ExecutionFileLimits | Unset = UNSET
    """Per-file and account storage limits for Run inputs and retained outputs."""
    max_contract_schema_bytes: int | Unset = UNSET
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

        artifact_deliveries = self.artifact_deliveries

        task_pipelines = self.task_pipelines

        run_controls: dict[str, Any] | Unset = UNSET
        if not isinstance(self.run_controls, Unset):
            run_controls = self.run_controls.to_dict()

        custom_environments = self.custom_environments

        environment_runtimes: list[str] | Unset = UNSET
        if not isinstance(self.environment_runtimes, Unset):
            environment_runtimes = []
            for environment_runtimes_item_data in self.environment_runtimes:
                environment_runtimes_item: str = environment_runtimes_item_data
                environment_runtimes.append(environment_runtimes_item)

        environment_limits: dict[str, Any] | Unset = UNSET
        if not isinstance(self.environment_limits, Unset):
            environment_limits = self.environment_limits.to_dict()

        execution_classes: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.execution_classes, Unset):
            execution_classes = []
            for execution_classes_item_data in self.execution_classes:
                execution_classes_item = execution_classes_item_data.to_dict()
                execution_classes.append(execution_classes_item)

        extended_admissions = self.extended_admissions

        file_limits: dict[str, Any] | Unset = UNSET
        if not isinstance(self.file_limits, Unset):
            file_limits = self.file_limits.to_dict()

        max_contract_schema_bytes = self.max_contract_schema_bytes

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
        if artifact_deliveries is not UNSET:
            field_dict["artifact_deliveries"] = artifact_deliveries
        if task_pipelines is not UNSET:
            field_dict["task_pipelines"] = task_pipelines
        if run_controls is not UNSET:
            field_dict["run_controls"] = run_controls
        if custom_environments is not UNSET:
            field_dict["custom_environments"] = custom_environments
        if environment_runtimes is not UNSET:
            field_dict["environment_runtimes"] = environment_runtimes
        if environment_limits is not UNSET:
            field_dict["environment_limits"] = environment_limits
        if execution_classes is not UNSET:
            field_dict["execution_classes"] = execution_classes
        if extended_admissions is not UNSET:
            field_dict["extended_admissions"] = extended_admissions
        if file_limits is not UNSET:
            field_dict["file_limits"] = file_limits
        if max_contract_schema_bytes is not UNSET:
            field_dict["max_contract_schema_bytes"] = max_contract_schema_bytes
        if unavailable_reasons is not UNSET:
            field_dict["unavailable_reasons"] = unavailable_reasons
        if limits is not UNSET:
            field_dict["limits"] = limits

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_capability_limits import ExecutionCapabilityLimits
        from ..models.execution_class_capability import ExecutionClassCapability
        from ..models.execution_environment_limits import ExecutionEnvironmentLimits
        from ..models.execution_file_limits import ExecutionFileLimits
        from ..models.execution_profile_capability import ExecutionProfileCapability
        from ..models.run_control_capabilities import RunControlCapabilities

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

        artifact_deliveries = d.pop("artifact_deliveries", UNSET)

        task_pipelines = d.pop("task_pipelines", UNSET)

        _run_controls = d.pop("run_controls", UNSET)
        run_controls: RunControlCapabilities | Unset
        if isinstance(_run_controls, Unset):
            run_controls = UNSET
        else:
            run_controls = RunControlCapabilities.from_dict(_run_controls)

        custom_environments = d.pop("custom_environments", UNSET)

        _environment_runtimes = d.pop("environment_runtimes", UNSET)
        environment_runtimes: list[ExecutionCapabilitiesResponseEnvironmentRuntimesItem] | Unset = UNSET
        if _environment_runtimes is not UNSET:
            environment_runtimes = []
            for environment_runtimes_item_data in _environment_runtimes:
                environment_runtimes_item = check_execution_capabilities_response_environment_runtimes_item(
                    environment_runtimes_item_data
                )

                environment_runtimes.append(environment_runtimes_item)

        _environment_limits = d.pop("environment_limits", UNSET)
        environment_limits: ExecutionEnvironmentLimits | Unset
        if isinstance(_environment_limits, Unset):
            environment_limits = UNSET
        else:
            environment_limits = ExecutionEnvironmentLimits.from_dict(_environment_limits)

        _execution_classes = d.pop("execution_classes", UNSET)
        execution_classes: list[ExecutionClassCapability] | Unset = UNSET
        if _execution_classes is not UNSET:
            execution_classes = []
            for execution_classes_item_data in _execution_classes:
                execution_classes_item = ExecutionClassCapability.from_dict(execution_classes_item_data)

                execution_classes.append(execution_classes_item)

        extended_admissions = d.pop("extended_admissions", UNSET)

        _file_limits = d.pop("file_limits", UNSET)
        file_limits: ExecutionFileLimits | Unset
        if isinstance(_file_limits, Unset):
            file_limits = UNSET
        else:
            file_limits = ExecutionFileLimits.from_dict(_file_limits)

        max_contract_schema_bytes = d.pop("max_contract_schema_bytes", UNSET)

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
            artifact_deliveries=artifact_deliveries,
            task_pipelines=task_pipelines,
            run_controls=run_controls,
            custom_environments=custom_environments,
            environment_runtimes=environment_runtimes,
            environment_limits=environment_limits,
            execution_classes=execution_classes,
            extended_admissions=extended_admissions,
            file_limits=file_limits,
            max_contract_schema_bytes=max_contract_schema_bytes,
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
