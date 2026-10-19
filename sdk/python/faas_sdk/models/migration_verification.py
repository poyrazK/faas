from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.migration_verification_phase import MigrationVerificationPhase, check_migration_verification_phase
from ..models.migration_verification_recovery_status import (
    MigrationVerificationRecoveryStatus,
    check_migration_verification_recovery_status,
)
from ..models.migration_verification_status import MigrationVerificationStatus, check_migration_verification_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_migration_verification_request import CreateMigrationVerificationRequest
    from ..models.migration_verification_check import MigrationVerificationCheck


T = TypeVar("T", bound="MigrationVerification")


@_attrs_define
class MigrationVerification:
    """Durable migration report tied to a deployment, configuration revision and optional cold-wake evidence."""

    id: UUID
    app: str
    deployment_id: UUID
    scope: str
    configuration_revision: str
    """Opaque fingerprint of deployment and scoped runtime configuration."""
    status: MigrationVerificationStatus
    phase: MigrationVerificationPhase
    request: CreateMigrationVerificationRequest
    """Immutable migration checks and explicit consent to park and wake the selected Gregale app."""
    checks: list[MigrationVerificationCheck]
    created_at: datetime.datetime
    expires_at: datetime.datetime
    reason: str | Unset = UNSET
    dependency_task_id: UUID | Unset = UNSET
    cold_dependency_task_id: UUID | Unset = UNSET
    wake_id: UUID | Unset = UNSET
    instance_id: UUID | Unset = UNSET
    drained_at: datetime.datetime | Unset = UNSET
    completed_at: datetime.datetime | Unset = UNSET
    cancel_requested_at: datetime.datetime | Unset = UNSET
    """Durable cancellation intent; pending status includes task shutdown and service recovery."""
    recovery_status: MigrationVerificationRecoveryStatus | Unset = UNSET
    """Restoration outcome after interrupted verification. Restored requires a running instance; superseded leaves
    the new deployment alone; blocked requires resolving an account or security restriction."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app = self.app

        deployment_id = str(self.deployment_id)

        scope = self.scope

        configuration_revision = self.configuration_revision

        status: str = self.status

        phase: str = self.phase

        request = self.request.to_dict()

        checks = []
        for checks_item_data in self.checks:
            checks_item = checks_item_data.to_dict()
            checks.append(checks_item)

        created_at = self.created_at.isoformat()

        expires_at = self.expires_at.isoformat()

        reason = self.reason

        dependency_task_id: str | Unset = UNSET
        if not isinstance(self.dependency_task_id, Unset):
            dependency_task_id = str(self.dependency_task_id)

        cold_dependency_task_id: str | Unset = UNSET
        if not isinstance(self.cold_dependency_task_id, Unset):
            cold_dependency_task_id = str(self.cold_dependency_task_id)

        wake_id: str | Unset = UNSET
        if not isinstance(self.wake_id, Unset):
            wake_id = str(self.wake_id)

        instance_id: str | Unset = UNSET
        if not isinstance(self.instance_id, Unset):
            instance_id = str(self.instance_id)

        drained_at: str | Unset = UNSET
        if not isinstance(self.drained_at, Unset):
            drained_at = self.drained_at.isoformat()

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        cancel_requested_at: str | Unset = UNSET
        if not isinstance(self.cancel_requested_at, Unset):
            cancel_requested_at = self.cancel_requested_at.isoformat()

        recovery_status: str | Unset = UNSET
        if not isinstance(self.recovery_status, Unset):
            recovery_status = self.recovery_status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app": app,
                "deployment_id": deployment_id,
                "scope": scope,
                "configuration_revision": configuration_revision,
                "status": status,
                "phase": phase,
                "request": request,
                "checks": checks,
                "created_at": created_at,
                "expires_at": expires_at,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if dependency_task_id is not UNSET:
            field_dict["dependency_task_id"] = dependency_task_id
        if cold_dependency_task_id is not UNSET:
            field_dict["cold_dependency_task_id"] = cold_dependency_task_id
        if wake_id is not UNSET:
            field_dict["wake_id"] = wake_id
        if instance_id is not UNSET:
            field_dict["instance_id"] = instance_id
        if drained_at is not UNSET:
            field_dict["drained_at"] = drained_at
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at
        if cancel_requested_at is not UNSET:
            field_dict["cancel_requested_at"] = cancel_requested_at
        if recovery_status is not UNSET:
            field_dict["recovery_status"] = recovery_status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_migration_verification_request import CreateMigrationVerificationRequest
        from ..models.migration_verification_check import MigrationVerificationCheck

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app = d.pop("app")

        deployment_id = UUID(d.pop("deployment_id"))

        scope = d.pop("scope")

        configuration_revision = d.pop("configuration_revision")

        status = check_migration_verification_status(d.pop("status"))

        phase = check_migration_verification_phase(d.pop("phase"))

        request = CreateMigrationVerificationRequest.from_dict(d.pop("request"))

        checks = []
        _checks = d.pop("checks")
        for checks_item_data in _checks:
            checks_item = MigrationVerificationCheck.from_dict(checks_item_data)

            checks.append(checks_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        reason = d.pop("reason", UNSET)

        _dependency_task_id = d.pop("dependency_task_id", UNSET)
        dependency_task_id: UUID | Unset
        if isinstance(_dependency_task_id, Unset):
            dependency_task_id = UNSET
        else:
            dependency_task_id = UUID(_dependency_task_id)

        _cold_dependency_task_id = d.pop("cold_dependency_task_id", UNSET)
        cold_dependency_task_id: UUID | Unset
        if isinstance(_cold_dependency_task_id, Unset):
            cold_dependency_task_id = UNSET
        else:
            cold_dependency_task_id = UUID(_cold_dependency_task_id)

        _wake_id = d.pop("wake_id", UNSET)
        wake_id: UUID | Unset
        if isinstance(_wake_id, Unset):
            wake_id = UNSET
        else:
            wake_id = UUID(_wake_id)

        _instance_id = d.pop("instance_id", UNSET)
        instance_id: UUID | Unset
        if isinstance(_instance_id, Unset):
            instance_id = UNSET
        else:
            instance_id = UUID(_instance_id)

        _drained_at = d.pop("drained_at", UNSET)
        drained_at: datetime.datetime | Unset
        if isinstance(_drained_at, Unset):
            drained_at = UNSET
        else:
            drained_at = datetime.datetime.fromisoformat(_drained_at)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        _cancel_requested_at = d.pop("cancel_requested_at", UNSET)
        cancel_requested_at: datetime.datetime | Unset
        if isinstance(_cancel_requested_at, Unset):
            cancel_requested_at = UNSET
        else:
            cancel_requested_at = datetime.datetime.fromisoformat(_cancel_requested_at)

        _recovery_status = d.pop("recovery_status", UNSET)
        recovery_status: MigrationVerificationRecoveryStatus | Unset
        if isinstance(_recovery_status, Unset):
            recovery_status = UNSET
        else:
            recovery_status = check_migration_verification_recovery_status(_recovery_status)

        migration_verification = cls(
            id=id,
            app=app,
            deployment_id=deployment_id,
            scope=scope,
            configuration_revision=configuration_revision,
            status=status,
            phase=phase,
            request=request,
            checks=checks,
            created_at=created_at,
            expires_at=expires_at,
            reason=reason,
            dependency_task_id=dependency_task_id,
            cold_dependency_task_id=cold_dependency_task_id,
            wake_id=wake_id,
            instance_id=instance_id,
            drained_at=drained_at,
            completed_at=completed_at,
            cancel_requested_at=cancel_requested_at,
            recovery_status=recovery_status,
        )

        migration_verification.additional_properties = d
        return migration_verification

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
