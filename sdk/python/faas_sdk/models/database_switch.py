from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.database_switch_phase import DatabaseSwitchPhase, check_database_switch_phase
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.database_switch_drain import DatabaseSwitchDrain


T = TypeVar("T", bound="DatabaseSwitch")


@_attrs_define
class DatabaseSwitch:
    """A rollback that restores the app's managed PostgreSQL database with the code (ADR-20261023181931). The database is
    restored to restore_point as a new database, and the app moves to it and to the target release while it is fenced.

    """

    id: UUID
    app_id: UUID
    scope: str
    """The deployment scope the rollback restores."""
    phase: DatabaseSwitchPhase
    """From fencing until completed the app does not serve. aborting unwinds everything done before the bindings
    switched."""
    target_deployment_id: UUID
    """The rollback target."""
    undone_deployment_ids: list[UUID]
    """Deployments whose release commands the restore undoes; rolling back to them is refused afterwards."""
    source_database_id: UUID
    """The database the app leaves. It keeps every write."""
    target_name: str
    """Name of the restored copy."""
    restore_point: datetime.datetime
    """Writes after this moment stay only in the source database."""
    drain: DatabaseSwitchDrain
    """A fenced app's instances by outcome: retired with a native receipt, parked (no process; snapshot
    invalidated), still pending, or blocking the switch."""
    phase_started_at: datetime.datetime
    created_at: datetime.datetime
    updated_at: datetime.datetime
    from_deployment_id: UUID | Unset = UNSET
    """The release serving when the switch was requested; the switch aborts if another one serves before it
    switches."""
    from_revision: int | Unset = UNSET
    target_revision: int | Unset = UNSET
    source_database_name: str | Unset = UNSET
    target_database_id: UUID | Unset = UNSET
    """The restored copy, once its restore has started."""
    failure_code: str | Unset = UNSET
    """Why a switch failed or was cancelled, or why it is aborting."""
    failure_detail: str | Unset = UNSET
    last_error_code: str | Unset = UNSET
    """The latest transient error the switch is retrying."""
    requested_by: str | Unset = UNSET
    cancel_requested_at: datetime.datetime | Unset = UNSET
    fenced_at: datetime.datetime | Unset = UNSET
    release_switched_at: datetime.datetime | Unset = UNSET
    activated_at: datetime.datetime | Unset = UNSET
    """When the bindings moved to the copy; there is no way back after it."""
    finished_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        scope = self.scope

        phase: str = self.phase

        target_deployment_id = str(self.target_deployment_id)

        undone_deployment_ids = []
        for undone_deployment_ids_item_data in self.undone_deployment_ids:
            undone_deployment_ids_item = str(undone_deployment_ids_item_data)
            undone_deployment_ids.append(undone_deployment_ids_item)

        source_database_id = str(self.source_database_id)

        target_name = self.target_name

        restore_point = self.restore_point.isoformat()

        drain = self.drain.to_dict()

        phase_started_at = self.phase_started_at.isoformat()

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        from_deployment_id: str | Unset = UNSET
        if not isinstance(self.from_deployment_id, Unset):
            from_deployment_id = str(self.from_deployment_id)

        from_revision = self.from_revision

        target_revision = self.target_revision

        source_database_name = self.source_database_name

        target_database_id: str | Unset = UNSET
        if not isinstance(self.target_database_id, Unset):
            target_database_id = str(self.target_database_id)

        failure_code = self.failure_code

        failure_detail = self.failure_detail

        last_error_code = self.last_error_code

        requested_by = self.requested_by

        cancel_requested_at: str | Unset = UNSET
        if not isinstance(self.cancel_requested_at, Unset):
            cancel_requested_at = self.cancel_requested_at.isoformat()

        fenced_at: str | Unset = UNSET
        if not isinstance(self.fenced_at, Unset):
            fenced_at = self.fenced_at.isoformat()

        release_switched_at: str | Unset = UNSET
        if not isinstance(self.release_switched_at, Unset):
            release_switched_at = self.release_switched_at.isoformat()

        activated_at: str | Unset = UNSET
        if not isinstance(self.activated_at, Unset):
            activated_at = self.activated_at.isoformat()

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "scope": scope,
                "phase": phase,
                "target_deployment_id": target_deployment_id,
                "undone_deployment_ids": undone_deployment_ids,
                "source_database_id": source_database_id,
                "target_name": target_name,
                "restore_point": restore_point,
                "drain": drain,
                "phase_started_at": phase_started_at,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if from_deployment_id is not UNSET:
            field_dict["from_deployment_id"] = from_deployment_id
        if from_revision is not UNSET:
            field_dict["from_revision"] = from_revision
        if target_revision is not UNSET:
            field_dict["target_revision"] = target_revision
        if source_database_name is not UNSET:
            field_dict["source_database_name"] = source_database_name
        if target_database_id is not UNSET:
            field_dict["target_database_id"] = target_database_id
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code
        if failure_detail is not UNSET:
            field_dict["failure_detail"] = failure_detail
        if last_error_code is not UNSET:
            field_dict["last_error_code"] = last_error_code
        if requested_by is not UNSET:
            field_dict["requested_by"] = requested_by
        if cancel_requested_at is not UNSET:
            field_dict["cancel_requested_at"] = cancel_requested_at
        if fenced_at is not UNSET:
            field_dict["fenced_at"] = fenced_at
        if release_switched_at is not UNSET:
            field_dict["release_switched_at"] = release_switched_at
        if activated_at is not UNSET:
            field_dict["activated_at"] = activated_at
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.database_switch_drain import DatabaseSwitchDrain

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        phase = check_database_switch_phase(d.pop("phase"))

        target_deployment_id = UUID(d.pop("target_deployment_id"))

        undone_deployment_ids = []
        _undone_deployment_ids = d.pop("undone_deployment_ids")
        for undone_deployment_ids_item_data in _undone_deployment_ids:
            undone_deployment_ids_item = UUID(undone_deployment_ids_item_data)

            undone_deployment_ids.append(undone_deployment_ids_item)

        source_database_id = UUID(d.pop("source_database_id"))

        target_name = d.pop("target_name")

        restore_point = datetime.datetime.fromisoformat(d.pop("restore_point"))

        drain = DatabaseSwitchDrain.from_dict(d.pop("drain"))

        phase_started_at = datetime.datetime.fromisoformat(d.pop("phase_started_at"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _from_deployment_id = d.pop("from_deployment_id", UNSET)
        from_deployment_id: UUID | Unset
        if isinstance(_from_deployment_id, Unset):
            from_deployment_id = UNSET
        else:
            from_deployment_id = UUID(_from_deployment_id)

        from_revision = d.pop("from_revision", UNSET)

        target_revision = d.pop("target_revision", UNSET)

        source_database_name = d.pop("source_database_name", UNSET)

        _target_database_id = d.pop("target_database_id", UNSET)
        target_database_id: UUID | Unset
        if isinstance(_target_database_id, Unset):
            target_database_id = UNSET
        else:
            target_database_id = UUID(_target_database_id)

        failure_code = d.pop("failure_code", UNSET)

        failure_detail = d.pop("failure_detail", UNSET)

        last_error_code = d.pop("last_error_code", UNSET)

        requested_by = d.pop("requested_by", UNSET)

        _cancel_requested_at = d.pop("cancel_requested_at", UNSET)
        cancel_requested_at: datetime.datetime | Unset
        if isinstance(_cancel_requested_at, Unset):
            cancel_requested_at = UNSET
        else:
            cancel_requested_at = datetime.datetime.fromisoformat(_cancel_requested_at)

        _fenced_at = d.pop("fenced_at", UNSET)
        fenced_at: datetime.datetime | Unset
        if isinstance(_fenced_at, Unset):
            fenced_at = UNSET
        else:
            fenced_at = datetime.datetime.fromisoformat(_fenced_at)

        _release_switched_at = d.pop("release_switched_at", UNSET)
        release_switched_at: datetime.datetime | Unset
        if isinstance(_release_switched_at, Unset):
            release_switched_at = UNSET
        else:
            release_switched_at = datetime.datetime.fromisoformat(_release_switched_at)

        _activated_at = d.pop("activated_at", UNSET)
        activated_at: datetime.datetime | Unset
        if isinstance(_activated_at, Unset):
            activated_at = UNSET
        else:
            activated_at = datetime.datetime.fromisoformat(_activated_at)

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        database_switch = cls(
            id=id,
            app_id=app_id,
            scope=scope,
            phase=phase,
            target_deployment_id=target_deployment_id,
            undone_deployment_ids=undone_deployment_ids,
            source_database_id=source_database_id,
            target_name=target_name,
            restore_point=restore_point,
            drain=drain,
            phase_started_at=phase_started_at,
            created_at=created_at,
            updated_at=updated_at,
            from_deployment_id=from_deployment_id,
            from_revision=from_revision,
            target_revision=target_revision,
            source_database_name=source_database_name,
            target_database_id=target_database_id,
            failure_code=failure_code,
            failure_detail=failure_detail,
            last_error_code=last_error_code,
            requested_by=requested_by,
            cancel_requested_at=cancel_requested_at,
            fenced_at=fenced_at,
            release_switched_at=release_switched_at,
            activated_at=activated_at,
            finished_at=finished_at,
        )

        database_switch.additional_properties = d
        return database_switch

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
