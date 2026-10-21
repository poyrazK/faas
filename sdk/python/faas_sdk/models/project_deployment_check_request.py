from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_deployment_check_workload import ProjectDeploymentCheckWorkload


T = TypeVar("T", bound="ProjectDeploymentCheckRequest")


@_attrs_define
class ProjectDeploymentCheckRequest:
    """Metadata-only project intent. Local source checks provide workloads; promotion checks provide from_environment
    instead. Proposed files never stage production changes.

    """

    environment: str | Unset = "production"
    from_environment: str | Unset = UNSET
    expected_state_hash: str | Unset = UNSET
    atomic: bool | Unset = UNSET
    add_services: bool | Unset = UNSET
    remove_services: bool | Unset = UNSET
    verify: bool | Unset = UNSET
    workers: bool | Unset = UNSET
    worker_health: bool | Unset = UNSET
    migrate: bool | Unset = UNSET
    progressive: bool | Unset = UNSET
    stabilize: bool | Unset = UNSET
    sync_config: bool | Unset = UNSET
    release_config: Any | Unset = UNSET
    """Complete variable sets and optional staged secret version references for selected workloads; omitted
    snapshots inherit prior release values."""
    workloads: list[ProjectDeploymentCheckWorkload] | Unset = UNSET
    managed: list[str] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        environment = self.environment

        from_environment = self.from_environment

        expected_state_hash = self.expected_state_hash

        atomic = self.atomic

        add_services = self.add_services

        remove_services = self.remove_services

        verify = self.verify

        workers = self.workers

        worker_health = self.worker_health

        migrate = self.migrate

        progressive = self.progressive

        stabilize = self.stabilize

        sync_config = self.sync_config

        release_config: Any | Unset
        if isinstance(self.release_config, Unset):
            release_config = UNSET
        else:
            release_config = self.release_config

        workloads: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.workloads, Unset):
            workloads = []
            for workloads_item_data in self.workloads:
                workloads_item = workloads_item_data.to_dict()
                workloads.append(workloads_item)

        managed: list[str] | Unset = UNSET
        if not isinstance(self.managed, Unset):
            managed = self.managed

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if environment is not UNSET:
            field_dict["environment"] = environment
        if from_environment is not UNSET:
            field_dict["from_environment"] = from_environment
        if expected_state_hash is not UNSET:
            field_dict["expected_state_hash"] = expected_state_hash
        if atomic is not UNSET:
            field_dict["atomic"] = atomic
        if add_services is not UNSET:
            field_dict["add_services"] = add_services
        if remove_services is not UNSET:
            field_dict["remove_services"] = remove_services
        if verify is not UNSET:
            field_dict["verify"] = verify
        if workers is not UNSET:
            field_dict["workers"] = workers
        if worker_health is not UNSET:
            field_dict["worker_health"] = worker_health
        if migrate is not UNSET:
            field_dict["migrate"] = migrate
        if progressive is not UNSET:
            field_dict["progressive"] = progressive
        if stabilize is not UNSET:
            field_dict["stabilize"] = stabilize
        if sync_config is not UNSET:
            field_dict["sync_config"] = sync_config
        if release_config is not UNSET:
            field_dict["release_config"] = release_config
        if workloads is not UNSET:
            field_dict["workloads"] = workloads
        if managed is not UNSET:
            field_dict["managed"] = managed

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_deployment_check_workload import ProjectDeploymentCheckWorkload

        d = dict(src_dict)
        environment = d.pop("environment", UNSET)

        from_environment = d.pop("from_environment", UNSET)

        expected_state_hash = d.pop("expected_state_hash", UNSET)

        atomic = d.pop("atomic", UNSET)

        add_services = d.pop("add_services", UNSET)

        remove_services = d.pop("remove_services", UNSET)

        verify = d.pop("verify", UNSET)

        workers = d.pop("workers", UNSET)

        worker_health = d.pop("worker_health", UNSET)

        migrate = d.pop("migrate", UNSET)

        progressive = d.pop("progressive", UNSET)

        stabilize = d.pop("stabilize", UNSET)

        sync_config = d.pop("sync_config", UNSET)

        def _parse_release_config(data: object) -> Any | Unset:
            if isinstance(data, Unset):
                return data
            return cast(Any | Unset, data)

        release_config = _parse_release_config(d.pop("release_config", UNSET))

        _workloads = d.pop("workloads", UNSET)
        workloads: list[ProjectDeploymentCheckWorkload] | Unset = UNSET
        if _workloads is not UNSET:
            workloads = []
            for workloads_item_data in _workloads:
                workloads_item = ProjectDeploymentCheckWorkload.from_dict(workloads_item_data)

                workloads.append(workloads_item)

        managed = cast(list[str], d.pop("managed", UNSET))

        project_deployment_check_request = cls(
            environment=environment,
            from_environment=from_environment,
            expected_state_hash=expected_state_hash,
            atomic=atomic,
            add_services=add_services,
            remove_services=remove_services,
            verify=verify,
            workers=workers,
            worker_health=worker_health,
            migrate=migrate,
            progressive=progressive,
            stabilize=stabilize,
            sync_config=sync_config,
            release_config=release_config,
            workloads=workloads,
            managed=managed,
        )

        return project_deployment_check_request
