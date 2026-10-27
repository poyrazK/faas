from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.register_scenario_test_request_action_evidence_version import (
    RegisterScenarioTestRequestActionEvidenceVersion,
    check_register_scenario_test_request_action_evidence_version,
)
from ..models.register_scenario_test_request_command_version import (
    RegisterScenarioTestRequestCommandVersion,
    check_register_scenario_test_request_command_version,
)
from ..models.register_scenario_test_request_network_mode import (
    RegisterScenarioTestRequestNetworkMode,
    check_register_scenario_test_request_network_mode,
)
from ..models.register_scenario_test_request_webhook_mode import (
    RegisterScenarioTestRequestWebhookMode,
    check_register_scenario_test_request_webhook_mode,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.scenario_cold_start_spec import ScenarioColdStartSpec
    from ..models.scenario_managed_work_plan import ScenarioManagedWorkPlan
    from ..models.scenario_simulation_plan import ScenarioSimulationPlan
    from ..models.scenario_suspend_resume_spec import ScenarioSuspendResumeSpec
    from ..models.scenario_test_workload import ScenarioTestWorkload


T = TypeVar("T", bound="RegisterScenarioTestRequest")


@_attrs_define
class RegisterScenarioTestRequest:
    """Complete set of developer sessions in one isolated scenario run."""

    members: list[ScenarioTestWorkload]
    suspend_resume: ScenarioSuspendResumeSpec | Unset = UNSET
    """One contained in-place pause after setup or at a cooperative managed-job checkpoint. No persistent VM
    snapshot is written."""
    cold_start: ScenarioColdStartSpec | Unset = UNSET
    """Versioned contained cold-start budget for every declared workload. Requires an operator-qualified cold-start
    fleet and excludes worker termination."""
    managed_work: ScenarioManagedWorkPlan | Unset = UNSET
    """Explicit participants and fixture permissions for managed async-route rehearsals. Requires contained
    commands and simulation; rollout is operator gated."""
    command_version: RegisterScenarioTestRequestCommandVersion | Unset = UNSET
    """Require acknowledgement of contained command execution before deployment. Requires simulation enrollment and
    an operator-qualified command fleet."""
    simulation: ScenarioSimulationPlan | Unset = UNSET
    """Immutable fixtures for contained serving apps using the managed outbound relay. Requires action evidence
    version 2. Canonical JSON is bounded to 64 KiB. No live provider fallback."""
    network_mode: RegisterScenarioTestRequestNetworkMode | Unset = UNSET
    """Opt in to runtime outbound containment version 1. Requires capture mode and an operator-qualified fleet.
    Supports same-run private HTTP and declared simulation adapters; other outbound traffic is blocked."""
    action_evidence_version: RegisterScenarioTestRequestActionEvidenceVersion | Unset = UNSET
    """Require support for sealed action evidence before deploying. Version 2 requires simulation fixtures. The
    server must echo this version in its acknowledgement. Requires webhook_mode capture."""
    webhook_mode: RegisterScenarioTestRequestWebhookMode | Unset = UNSET
    """Capture managed webhook effects and suppress all other webhook deliveries from these fresh test apps.
    Requires no existing deployments or work. Does not intercept direct application HTTP calls."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        members = []
        for members_item_data in self.members:
            members_item = members_item_data.to_dict()
            members.append(members_item)

        suspend_resume: dict[str, Any] | Unset = UNSET
        if not isinstance(self.suspend_resume, Unset):
            suspend_resume = self.suspend_resume.to_dict()

        cold_start: dict[str, Any] | Unset = UNSET
        if not isinstance(self.cold_start, Unset):
            cold_start = self.cold_start.to_dict()

        managed_work: dict[str, Any] | Unset = UNSET
        if not isinstance(self.managed_work, Unset):
            managed_work = self.managed_work.to_dict()

        command_version: int | Unset = UNSET
        if not isinstance(self.command_version, Unset):
            command_version = self.command_version

        simulation: dict[str, Any] | Unset = UNSET
        if not isinstance(self.simulation, Unset):
            simulation = self.simulation.to_dict()

        network_mode: str | Unset = UNSET
        if not isinstance(self.network_mode, Unset):
            network_mode = self.network_mode

        action_evidence_version: int | Unset = UNSET
        if not isinstance(self.action_evidence_version, Unset):
            action_evidence_version = self.action_evidence_version

        webhook_mode: str | Unset = UNSET
        if not isinstance(self.webhook_mode, Unset):
            webhook_mode = self.webhook_mode

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "members": members,
            }
        )
        if suspend_resume is not UNSET:
            field_dict["suspend_resume"] = suspend_resume
        if cold_start is not UNSET:
            field_dict["cold_start"] = cold_start
        if managed_work is not UNSET:
            field_dict["managed_work"] = managed_work
        if command_version is not UNSET:
            field_dict["command_version"] = command_version
        if simulation is not UNSET:
            field_dict["simulation"] = simulation
        if network_mode is not UNSET:
            field_dict["network_mode"] = network_mode
        if action_evidence_version is not UNSET:
            field_dict["action_evidence_version"] = action_evidence_version
        if webhook_mode is not UNSET:
            field_dict["webhook_mode"] = webhook_mode

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.scenario_cold_start_spec import ScenarioColdStartSpec
        from ..models.scenario_managed_work_plan import ScenarioManagedWorkPlan
        from ..models.scenario_simulation_plan import ScenarioSimulationPlan
        from ..models.scenario_suspend_resume_spec import ScenarioSuspendResumeSpec
        from ..models.scenario_test_workload import ScenarioTestWorkload

        d = dict(src_dict)
        members = []
        _members = d.pop("members")
        for members_item_data in _members:
            members_item = ScenarioTestWorkload.from_dict(members_item_data)

            members.append(members_item)

        _suspend_resume = d.pop("suspend_resume", UNSET)
        suspend_resume: ScenarioSuspendResumeSpec | Unset
        if isinstance(_suspend_resume, Unset):
            suspend_resume = UNSET
        else:
            suspend_resume = ScenarioSuspendResumeSpec.from_dict(_suspend_resume)

        _cold_start = d.pop("cold_start", UNSET)
        cold_start: ScenarioColdStartSpec | Unset
        if isinstance(_cold_start, Unset):
            cold_start = UNSET
        else:
            cold_start = ScenarioColdStartSpec.from_dict(_cold_start)

        _managed_work = d.pop("managed_work", UNSET)
        managed_work: ScenarioManagedWorkPlan | Unset
        if isinstance(_managed_work, Unset):
            managed_work = UNSET
        else:
            managed_work = ScenarioManagedWorkPlan.from_dict(_managed_work)

        _command_version = d.pop("command_version", UNSET)
        command_version: RegisterScenarioTestRequestCommandVersion | Unset
        if isinstance(_command_version, Unset):
            command_version = UNSET
        else:
            command_version = check_register_scenario_test_request_command_version(_command_version)

        _simulation = d.pop("simulation", UNSET)
        simulation: ScenarioSimulationPlan | Unset
        if isinstance(_simulation, Unset):
            simulation = UNSET
        else:
            simulation = ScenarioSimulationPlan.from_dict(_simulation)

        _network_mode = d.pop("network_mode", UNSET)
        network_mode: RegisterScenarioTestRequestNetworkMode | Unset
        if isinstance(_network_mode, Unset):
            network_mode = UNSET
        else:
            network_mode = check_register_scenario_test_request_network_mode(_network_mode)

        _action_evidence_version = d.pop("action_evidence_version", UNSET)
        action_evidence_version: RegisterScenarioTestRequestActionEvidenceVersion | Unset
        if isinstance(_action_evidence_version, Unset):
            action_evidence_version = UNSET
        else:
            action_evidence_version = check_register_scenario_test_request_action_evidence_version(
                _action_evidence_version
            )

        _webhook_mode = d.pop("webhook_mode", UNSET)
        webhook_mode: RegisterScenarioTestRequestWebhookMode | Unset
        if isinstance(_webhook_mode, Unset):
            webhook_mode = UNSET
        else:
            webhook_mode = check_register_scenario_test_request_webhook_mode(_webhook_mode)

        register_scenario_test_request = cls(
            members=members,
            suspend_resume=suspend_resume,
            cold_start=cold_start,
            managed_work=managed_work,
            command_version=command_version,
            simulation=simulation,
            network_mode=network_mode,
            action_evidence_version=action_evidence_version,
            webhook_mode=webhook_mode,
        )

        register_scenario_test_request.additional_properties = d
        return register_scenario_test_request

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
