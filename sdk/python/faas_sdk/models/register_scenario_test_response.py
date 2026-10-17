from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.register_scenario_test_response_action_evidence_version import (
    RegisterScenarioTestResponseActionEvidenceVersion,
    check_register_scenario_test_response_action_evidence_version,
)
from ..models.register_scenario_test_response_command_version import (
    RegisterScenarioTestResponseCommandVersion,
    check_register_scenario_test_response_command_version,
)
from ..models.register_scenario_test_response_network_mode import (
    RegisterScenarioTestResponseNetworkMode,
    check_register_scenario_test_response_network_mode,
)
from ..models.register_scenario_test_response_policy_version import (
    RegisterScenarioTestResponsePolicyVersion,
    check_register_scenario_test_response_policy_version,
)
from ..models.register_scenario_test_response_recorder_version import (
    RegisterScenarioTestResponseRecorderVersion,
    check_register_scenario_test_response_recorder_version,
)
from ..models.register_scenario_test_response_webhook_mode import (
    RegisterScenarioTestResponseWebhookMode,
    check_register_scenario_test_response_webhook_mode,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RegisterScenarioTestResponse")


@_attrs_define
class RegisterScenarioTestResponse:
    """Acknowledgement that the versioned webhook capture policy was committed before source deployment."""

    webhook_mode: RegisterScenarioTestResponseWebhookMode
    policy_version: RegisterScenarioTestResponsePolicyVersion
    suspend_resume_sha256: str | Unset = UNSET
    """Acknowledges the exact suspend/resume plan and workload inventory before deployment."""
    cold_start_sha256: str | Unset = UNSET
    """Acknowledges the exact cold-start budget and workload inventory before deployment."""
    managed_work_sha256: str | Unset = UNSET
    """Acknowledges the exact durable participant plan before deployment."""
    command_version: RegisterScenarioTestResponseCommandVersion | Unset = UNSET
    """Echoes supported contained command execution. Each command also requires a matching guest receipt and owner
    retirement before success."""
    recorder_version: RegisterScenarioTestResponseRecorderVersion | Unset = UNSET
    """Required when simulation is enrolled. Acknowledges durable recorder support before source deployment."""
    simulation_sha256: str | Unset = UNSET
    """Echoes the canonical fixture plan identity after durable enrollment."""
    network_mode: RegisterScenarioTestResponseNetworkMode | Unset = UNSET
    """Echoes the durable network containment enrollment. Each runtime must also acknowledge the exact run before
    publication."""
    action_evidence_version: RegisterScenarioTestResponseActionEvidenceVersion | Unset = UNSET
    """Present when the requested sealed action-evidence protocol is supported."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        webhook_mode: str = self.webhook_mode

        policy_version: int = self.policy_version

        suspend_resume_sha256 = self.suspend_resume_sha256

        cold_start_sha256 = self.cold_start_sha256

        managed_work_sha256 = self.managed_work_sha256

        command_version: int | Unset = UNSET
        if not isinstance(self.command_version, Unset):
            command_version = self.command_version

        recorder_version: int | Unset = UNSET
        if not isinstance(self.recorder_version, Unset):
            recorder_version = self.recorder_version

        simulation_sha256 = self.simulation_sha256

        network_mode: str | Unset = UNSET
        if not isinstance(self.network_mode, Unset):
            network_mode = self.network_mode

        action_evidence_version: int | Unset = UNSET
        if not isinstance(self.action_evidence_version, Unset):
            action_evidence_version = self.action_evidence_version

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "webhook_mode": webhook_mode,
                "policy_version": policy_version,
            }
        )
        if suspend_resume_sha256 is not UNSET:
            field_dict["suspend_resume_sha256"] = suspend_resume_sha256
        if cold_start_sha256 is not UNSET:
            field_dict["cold_start_sha256"] = cold_start_sha256
        if managed_work_sha256 is not UNSET:
            field_dict["managed_work_sha256"] = managed_work_sha256
        if command_version is not UNSET:
            field_dict["command_version"] = command_version
        if recorder_version is not UNSET:
            field_dict["recorder_version"] = recorder_version
        if simulation_sha256 is not UNSET:
            field_dict["simulation_sha256"] = simulation_sha256
        if network_mode is not UNSET:
            field_dict["network_mode"] = network_mode
        if action_evidence_version is not UNSET:
            field_dict["action_evidence_version"] = action_evidence_version

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        webhook_mode = check_register_scenario_test_response_webhook_mode(d.pop("webhook_mode"))

        policy_version = check_register_scenario_test_response_policy_version(d.pop("policy_version"))

        suspend_resume_sha256 = d.pop("suspend_resume_sha256", UNSET)

        cold_start_sha256 = d.pop("cold_start_sha256", UNSET)

        managed_work_sha256 = d.pop("managed_work_sha256", UNSET)

        _command_version = d.pop("command_version", UNSET)
        command_version: RegisterScenarioTestResponseCommandVersion | Unset
        if isinstance(_command_version, Unset):
            command_version = UNSET
        else:
            command_version = check_register_scenario_test_response_command_version(_command_version)

        _recorder_version = d.pop("recorder_version", UNSET)
        recorder_version: RegisterScenarioTestResponseRecorderVersion | Unset
        if isinstance(_recorder_version, Unset):
            recorder_version = UNSET
        else:
            recorder_version = check_register_scenario_test_response_recorder_version(_recorder_version)

        simulation_sha256 = d.pop("simulation_sha256", UNSET)

        _network_mode = d.pop("network_mode", UNSET)
        network_mode: RegisterScenarioTestResponseNetworkMode | Unset
        if isinstance(_network_mode, Unset):
            network_mode = UNSET
        else:
            network_mode = check_register_scenario_test_response_network_mode(_network_mode)

        _action_evidence_version = d.pop("action_evidence_version", UNSET)
        action_evidence_version: RegisterScenarioTestResponseActionEvidenceVersion | Unset
        if isinstance(_action_evidence_version, Unset):
            action_evidence_version = UNSET
        else:
            action_evidence_version = check_register_scenario_test_response_action_evidence_version(
                _action_evidence_version
            )

        register_scenario_test_response = cls(
            webhook_mode=webhook_mode,
            policy_version=policy_version,
            suspend_resume_sha256=suspend_resume_sha256,
            cold_start_sha256=cold_start_sha256,
            managed_work_sha256=managed_work_sha256,
            command_version=command_version,
            recorder_version=recorder_version,
            simulation_sha256=simulation_sha256,
            network_mode=network_mode,
            action_evidence_version=action_evidence_version,
        )

        register_scenario_test_response.additional_properties = d
        return register_scenario_test_response

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
