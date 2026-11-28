from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_release_verification_actions_item import (
    ProjectReleaseVerificationActionsItem,
    check_project_release_verification_actions_item,
)
from ..models.project_release_verification_percent import (
    ProjectReleaseVerificationPercent,
    check_project_release_verification_percent,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_release_control import ProjectReleaseControl
    from ..models.project_release_probe import ProjectReleaseProbe
    from ..models.project_release_probe_evidence import ProjectReleaseProbeEvidence
    from ..models.project_release_stabilization import ProjectReleaseStabilization
    from ..models.project_worker_health import ProjectWorkerHealth


T = TypeVar("T", bound="ProjectReleaseVerification")


@_attrs_define
class ProjectReleaseVerification:
    """Snapshotted critical probes and durable post-cutover verification evidence."""

    probes: list[ProjectReleaseProbe]
    healthy_rounds: int
    regressed_rounds: int
    stabilization: ProjectReleaseStabilization | Unset = UNSET
    """Opt-in bounded health monitoring after progressive rollout success."""
    control: ProjectReleaseControl | Unset = UNSET
    """Most recent durable operator decision and its restoration outcome."""
    actions: list[ProjectReleaseVerificationActionsItem] | Unset = UNSET
    """Currently available guarded controls; an empty list permits inspection only."""
    progressive: bool | Unset = UNSET
    """Require staged 5 percent, 25 percent and full project promotion."""
    percent: ProjectReleaseVerificationPercent | Unset = UNSET
    """Current progressive stage traffic percentage."""
    generation: int | Unset = UNSET
    """Durable progressive observation generation."""
    worker_health: ProjectWorkerHealth | Unset = UNSET
    """Opt-in real production queue delivery verification; absence of traffic never proves health."""
    deadline: datetime.datetime | Unset = UNSET
    checked_at: datetime.datetime | Unset = UNSET
    evidence: list[ProjectReleaseProbeEvidence] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        probes = []
        for probes_item_data in self.probes:
            probes_item = probes_item_data.to_dict()
            probes.append(probes_item)

        healthy_rounds = self.healthy_rounds

        regressed_rounds = self.regressed_rounds

        stabilization: dict[str, Any] | Unset = UNSET
        if not isinstance(self.stabilization, Unset):
            stabilization = self.stabilization.to_dict()

        control: dict[str, Any] | Unset = UNSET
        if not isinstance(self.control, Unset):
            control = self.control.to_dict()

        actions: list[str] | Unset = UNSET
        if not isinstance(self.actions, Unset):
            actions = []
            for actions_item_data in self.actions:
                actions_item: str = actions_item_data
                actions.append(actions_item)

        progressive = self.progressive

        percent: int | Unset = UNSET
        if not isinstance(self.percent, Unset):
            percent = self.percent

        generation = self.generation

        worker_health: dict[str, Any] | Unset = UNSET
        if not isinstance(self.worker_health, Unset):
            worker_health = self.worker_health.to_dict()

        deadline: str | Unset = UNSET
        if not isinstance(self.deadline, Unset):
            deadline = self.deadline.isoformat()

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        evidence: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.evidence, Unset):
            evidence = []
            for evidence_item_data in self.evidence:
                evidence_item = evidence_item_data.to_dict()
                evidence.append(evidence_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "probes": probes,
                "healthy_rounds": healthy_rounds,
                "regressed_rounds": regressed_rounds,
            }
        )
        if stabilization is not UNSET:
            field_dict["stabilization"] = stabilization
        if control is not UNSET:
            field_dict["control"] = control
        if actions is not UNSET:
            field_dict["actions"] = actions
        if progressive is not UNSET:
            field_dict["progressive"] = progressive
        if percent is not UNSET:
            field_dict["percent"] = percent
        if generation is not UNSET:
            field_dict["generation"] = generation
        if worker_health is not UNSET:
            field_dict["worker_health"] = worker_health
        if deadline is not UNSET:
            field_dict["deadline"] = deadline
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if evidence is not UNSET:
            field_dict["evidence"] = evidence

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_release_control import ProjectReleaseControl
        from ..models.project_release_probe import ProjectReleaseProbe
        from ..models.project_release_probe_evidence import ProjectReleaseProbeEvidence
        from ..models.project_release_stabilization import ProjectReleaseStabilization
        from ..models.project_worker_health import ProjectWorkerHealth

        d = dict(src_dict)
        probes = []
        _probes = d.pop("probes")
        for probes_item_data in _probes:
            probes_item = ProjectReleaseProbe.from_dict(probes_item_data)

            probes.append(probes_item)

        healthy_rounds = d.pop("healthy_rounds")

        regressed_rounds = d.pop("regressed_rounds")

        _stabilization = d.pop("stabilization", UNSET)
        stabilization: ProjectReleaseStabilization | Unset
        if isinstance(_stabilization, Unset):
            stabilization = UNSET
        else:
            stabilization = ProjectReleaseStabilization.from_dict(_stabilization)

        _control = d.pop("control", UNSET)
        control: ProjectReleaseControl | Unset
        if isinstance(_control, Unset):
            control = UNSET
        else:
            control = ProjectReleaseControl.from_dict(_control)

        _actions = d.pop("actions", UNSET)
        actions: list[ProjectReleaseVerificationActionsItem] | Unset = UNSET
        if _actions is not UNSET:
            actions = []
            for actions_item_data in _actions:
                actions_item = check_project_release_verification_actions_item(actions_item_data)

                actions.append(actions_item)

        progressive = d.pop("progressive", UNSET)

        _percent = d.pop("percent", UNSET)
        percent: ProjectReleaseVerificationPercent | Unset
        if isinstance(_percent, Unset):
            percent = UNSET
        else:
            percent = check_project_release_verification_percent(_percent)

        generation = d.pop("generation", UNSET)

        _worker_health = d.pop("worker_health", UNSET)
        worker_health: ProjectWorkerHealth | Unset
        if isinstance(_worker_health, Unset):
            worker_health = UNSET
        else:
            worker_health = ProjectWorkerHealth.from_dict(_worker_health)

        _deadline = d.pop("deadline", UNSET)
        deadline: datetime.datetime | Unset
        if isinstance(_deadline, Unset):
            deadline = UNSET
        else:
            deadline = datetime.datetime.fromisoformat(_deadline)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        _evidence = d.pop("evidence", UNSET)
        evidence: list[ProjectReleaseProbeEvidence] | Unset = UNSET
        if _evidence is not UNSET:
            evidence = []
            for evidence_item_data in _evidence:
                evidence_item = ProjectReleaseProbeEvidence.from_dict(evidence_item_data)

                evidence.append(evidence_item)

        project_release_verification = cls(
            probes=probes,
            healthy_rounds=healthy_rounds,
            regressed_rounds=regressed_rounds,
            stabilization=stabilization,
            control=control,
            actions=actions,
            progressive=progressive,
            percent=percent,
            generation=generation,
            worker_health=worker_health,
            deadline=deadline,
            checked_at=checked_at,
            evidence=evidence,
        )

        project_release_verification.additional_properties = d
        return project_release_verification

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
