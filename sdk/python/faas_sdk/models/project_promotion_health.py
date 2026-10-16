from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.project_promotion_health_phase import ProjectPromotionHealthPhase, check_project_promotion_health_phase
from ..models.project_promotion_health_verdict import (
    ProjectPromotionHealthVerdict,
    check_project_promotion_health_verdict,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_promotion_admission import ProjectPromotionAdmission
    from ..models.project_promotion_control import ProjectPromotionControl
    from ..models.project_promotion_health_policy import ProjectPromotionHealthPolicy
    from ..models.project_promotion_migration_recovery import ProjectPromotionMigrationRecovery
    from ..models.project_promotion_worker_health import ProjectPromotionWorkerHealth
    from ..models.project_release_command import ProjectReleaseCommand
    from ..models.project_release_probe_evidence import ProjectReleaseProbeEvidence
    from ..models.project_release_worker import ProjectReleaseWorker
    from ..models.project_worker_health import ProjectWorkerHealth


T = TypeVar("T", bound="ProjectPromotionHealth")


@_attrs_define
class ProjectPromotionHealth:
    """Durable destination health checkpoint. Includes counts only, without challenge tokens, credentials or response
    bodies.

    """

    policy: ProjectPromotionHealthPolicy
    """Destination probe and optional migration contract bound to the exact promotion preview and approval."""
    phase: ProjectPromotionHealthPhase
    generation: int
    started_at: datetime.datetime
    """Queue entry time while waiting; reset once to execution start at validated dispatch."""
    deadline: datetime.datetime
    """Queue wait deadline while queued; a fresh execution deadline after dispatch."""
    verdict: ProjectPromotionHealthVerdict
    healthy_rounds: int
    regressed_rounds: int
    workers: list[ProjectPromotionWorkerHealth] | Unset = UNSET
    admission: ProjectPromotionAdmission | Unset = UNSET
    """Durable capacity wait receipt. Queue time does not consume the execution health window. An absent started_at
    means execution has never started."""
    control: ProjectPromotionControl | Unset = UNSET
    """Durable promotion abort or restoration decision, including its operator, deadline and resolution."""
    worker: ProjectReleaseWorker | Unset = UNSET
    """One graph-owned production push worker, its immutable binding intent and runtime admission evidence."""
    worker_health: ProjectWorkerHealth | Unset = UNSET
    """Opt-in real production queue delivery verification; absence of traffic never proves health."""
    recovery: ProjectPromotionMigrationRecovery | Unset = UNSET
    """Durable acknowledgement and deadline for an operator's promotion migration recovery decision."""
    migration: ProjectReleaseCommand | Unset = UNSET
    """Planned backward-compatible migration identity and durable task outcome. Task logs remain on the app-task
    resource."""
    checked_at: datetime.datetime | Unset = UNSET
    message: str | Unset = UNSET
    evidence: list[ProjectReleaseProbeEvidence] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        policy = self.policy.to_dict()

        phase: str = self.phase

        generation = self.generation

        started_at = self.started_at.isoformat()

        deadline = self.deadline.isoformat()

        verdict: str = self.verdict

        healthy_rounds = self.healthy_rounds

        regressed_rounds = self.regressed_rounds

        workers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.workers, Unset):
            workers = []
            for workers_item_data in self.workers:
                workers_item = workers_item_data.to_dict()
                workers.append(workers_item)

        admission: dict[str, Any] | Unset = UNSET
        if not isinstance(self.admission, Unset):
            admission = self.admission.to_dict()

        control: dict[str, Any] | Unset = UNSET
        if not isinstance(self.control, Unset):
            control = self.control.to_dict()

        worker: dict[str, Any] | Unset = UNSET
        if not isinstance(self.worker, Unset):
            worker = self.worker.to_dict()

        worker_health: dict[str, Any] | Unset = UNSET
        if not isinstance(self.worker_health, Unset):
            worker_health = self.worker_health.to_dict()

        recovery: dict[str, Any] | Unset = UNSET
        if not isinstance(self.recovery, Unset):
            recovery = self.recovery.to_dict()

        migration: dict[str, Any] | Unset = UNSET
        if not isinstance(self.migration, Unset):
            migration = self.migration.to_dict()

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        message = self.message

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
                "policy": policy,
                "phase": phase,
                "generation": generation,
                "started_at": started_at,
                "deadline": deadline,
                "verdict": verdict,
                "healthy_rounds": healthy_rounds,
                "regressed_rounds": regressed_rounds,
            }
        )
        if workers is not UNSET:
            field_dict["workers"] = workers
        if admission is not UNSET:
            field_dict["admission"] = admission
        if control is not UNSET:
            field_dict["control"] = control
        if worker is not UNSET:
            field_dict["worker"] = worker
        if worker_health is not UNSET:
            field_dict["worker_health"] = worker_health
        if recovery is not UNSET:
            field_dict["recovery"] = recovery
        if migration is not UNSET:
            field_dict["migration"] = migration
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if message is not UNSET:
            field_dict["message"] = message
        if evidence is not UNSET:
            field_dict["evidence"] = evidence

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_promotion_admission import ProjectPromotionAdmission
        from ..models.project_promotion_control import ProjectPromotionControl
        from ..models.project_promotion_health_policy import ProjectPromotionHealthPolicy
        from ..models.project_promotion_migration_recovery import ProjectPromotionMigrationRecovery
        from ..models.project_promotion_worker_health import ProjectPromotionWorkerHealth
        from ..models.project_release_command import ProjectReleaseCommand
        from ..models.project_release_probe_evidence import ProjectReleaseProbeEvidence
        from ..models.project_release_worker import ProjectReleaseWorker
        from ..models.project_worker_health import ProjectWorkerHealth

        d = dict(src_dict)
        policy = ProjectPromotionHealthPolicy.from_dict(d.pop("policy"))

        phase = check_project_promotion_health_phase(d.pop("phase"))

        generation = d.pop("generation")

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        deadline = datetime.datetime.fromisoformat(d.pop("deadline"))

        verdict = check_project_promotion_health_verdict(d.pop("verdict"))

        healthy_rounds = d.pop("healthy_rounds")

        regressed_rounds = d.pop("regressed_rounds")

        _workers = d.pop("workers", UNSET)
        workers: list[ProjectPromotionWorkerHealth] | Unset = UNSET
        if _workers is not UNSET:
            workers = []
            for workers_item_data in _workers:
                workers_item = ProjectPromotionWorkerHealth.from_dict(workers_item_data)

                workers.append(workers_item)

        _admission = d.pop("admission", UNSET)
        admission: ProjectPromotionAdmission | Unset
        if isinstance(_admission, Unset):
            admission = UNSET
        else:
            admission = ProjectPromotionAdmission.from_dict(_admission)

        _control = d.pop("control", UNSET)
        control: ProjectPromotionControl | Unset
        if isinstance(_control, Unset):
            control = UNSET
        else:
            control = ProjectPromotionControl.from_dict(_control)

        _worker = d.pop("worker", UNSET)
        worker: ProjectReleaseWorker | Unset
        if isinstance(_worker, Unset):
            worker = UNSET
        else:
            worker = ProjectReleaseWorker.from_dict(_worker)

        _worker_health = d.pop("worker_health", UNSET)
        worker_health: ProjectWorkerHealth | Unset
        if isinstance(_worker_health, Unset):
            worker_health = UNSET
        else:
            worker_health = ProjectWorkerHealth.from_dict(_worker_health)

        _recovery = d.pop("recovery", UNSET)
        recovery: ProjectPromotionMigrationRecovery | Unset
        if isinstance(_recovery, Unset):
            recovery = UNSET
        else:
            recovery = ProjectPromotionMigrationRecovery.from_dict(_recovery)

        _migration = d.pop("migration", UNSET)
        migration: ProjectReleaseCommand | Unset
        if isinstance(_migration, Unset):
            migration = UNSET
        else:
            migration = ProjectReleaseCommand.from_dict(_migration)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        message = d.pop("message", UNSET)

        _evidence = d.pop("evidence", UNSET)
        evidence: list[ProjectReleaseProbeEvidence] | Unset = UNSET
        if _evidence is not UNSET:
            evidence = []
            for evidence_item_data in _evidence:
                evidence_item = ProjectReleaseProbeEvidence.from_dict(evidence_item_data)

                evidence.append(evidence_item)

        project_promotion_health = cls(
            policy=policy,
            phase=phase,
            generation=generation,
            started_at=started_at,
            deadline=deadline,
            verdict=verdict,
            healthy_rounds=healthy_rounds,
            regressed_rounds=regressed_rounds,
            workers=workers,
            admission=admission,
            control=control,
            worker=worker,
            worker_health=worker_health,
            recovery=recovery,
            migration=migration,
            checked_at=checked_at,
            message=message,
            evidence=evidence,
        )

        project_promotion_health.additional_properties = d
        return project_promotion_health

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
