from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.worker_rollout_observation_basis import (
    WorkerRolloutObservationBasis,
    check_worker_rollout_observation_basis,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkerRolloutObservation")


@_attrs_define
class WorkerRolloutObservation:
    """Passive deployment-attributed observations of the selected worker generation."""

    app_id: str
    deployment_id: str
    scope: str
    checked_at: datetime.datetime
    basis: WorkerRolloutObservationBasis
    running: int
    starting: int
    draining: int
    other_residents: int
    retired_residents: int
    generation_converged: bool
    queue_processing_confirmed: bool
    project_id: str | Unset = UNSET
    environment_id: str | Unset = UNSET
    selected_deployment_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        deployment_id = self.deployment_id

        scope = self.scope

        checked_at = self.checked_at.isoformat()

        basis: str = self.basis

        running = self.running

        starting = self.starting

        draining = self.draining

        other_residents = self.other_residents

        retired_residents = self.retired_residents

        generation_converged = self.generation_converged

        queue_processing_confirmed = self.queue_processing_confirmed

        project_id = self.project_id

        environment_id = self.environment_id

        selected_deployment_id = self.selected_deployment_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "deployment_id": deployment_id,
                "scope": scope,
                "checked_at": checked_at,
                "basis": basis,
                "running": running,
                "starting": starting,
                "draining": draining,
                "other_residents": other_residents,
                "retired_residents": retired_residents,
                "generation_converged": generation_converged,
                "queue_processing_confirmed": queue_processing_confirmed,
            }
        )
        if project_id is not UNSET:
            field_dict["project_id"] = project_id
        if environment_id is not UNSET:
            field_dict["environment_id"] = environment_id
        if selected_deployment_id is not UNSET:
            field_dict["selected_deployment_id"] = selected_deployment_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        deployment_id = d.pop("deployment_id")

        scope = d.pop("scope")

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        basis = check_worker_rollout_observation_basis(d.pop("basis"))

        running = d.pop("running")

        starting = d.pop("starting")

        draining = d.pop("draining")

        other_residents = d.pop("other_residents")

        retired_residents = d.pop("retired_residents")

        generation_converged = d.pop("generation_converged")

        queue_processing_confirmed = d.pop("queue_processing_confirmed")

        project_id = d.pop("project_id", UNSET)

        environment_id = d.pop("environment_id", UNSET)

        selected_deployment_id = d.pop("selected_deployment_id", UNSET)

        worker_rollout_observation = cls(
            app_id=app_id,
            deployment_id=deployment_id,
            scope=scope,
            checked_at=checked_at,
            basis=basis,
            running=running,
            starting=starting,
            draining=draining,
            other_residents=other_residents,
            retired_residents=retired_residents,
            generation_converged=generation_converged,
            queue_processing_confirmed=queue_processing_confirmed,
            project_id=project_id,
            environment_id=environment_id,
            selected_deployment_id=selected_deployment_id,
        )

        worker_rollout_observation.additional_properties = d
        return worker_rollout_observation

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
