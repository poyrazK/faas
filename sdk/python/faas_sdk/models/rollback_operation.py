from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.rollback_operation_status import RollbackOperationStatus, check_rollback_operation_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.binding_check_finding import BindingCheckFinding


T = TypeVar("T", bound="RollbackOperation")


@_attrs_define
class RollbackOperation:
    """Durable progress of a checked historical rollback. This receipt grants no authority to reuse binding evidence or
    move traffic for another operation.

    """

    id: UUID
    app_id: UUID
    scope: str
    target_deployment_id: UUID
    current_deployment_id: UUID
    status: RollbackOperationStatus
    service: bool
    created_at: datetime.datetime
    updated_at: datetime.datetime
    reason: str | Unset = UNSET
    code: str | Unset = UNSET
    blockers: list[BindingCheckFinding] | Unset = UNSET
    completed_at: datetime.datetime | None | Unset = UNSET
    audit_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        scope = self.scope

        target_deployment_id = str(self.target_deployment_id)

        current_deployment_id = str(self.current_deployment_id)

        status: str = self.status

        service = self.service

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        reason = self.reason

        code = self.code

        blockers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.blockers, Unset):
            blockers = []
            for blockers_item_data in self.blockers:
                blockers_item = blockers_item_data.to_dict()
                blockers.append(blockers_item)

        completed_at: None | str | Unset
        if isinstance(self.completed_at, Unset):
            completed_at = UNSET
        elif isinstance(self.completed_at, datetime.datetime):
            completed_at = self.completed_at.isoformat()
        else:
            completed_at = self.completed_at

        audit_id = self.audit_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "scope": scope,
                "target_deployment_id": target_deployment_id,
                "current_deployment_id": current_deployment_id,
                "status": status,
                "service": service,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if code is not UNSET:
            field_dict["code"] = code
        if blockers is not UNSET:
            field_dict["blockers"] = blockers
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at
        if audit_id is not UNSET:
            field_dict["audit_id"] = audit_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_check_finding import BindingCheckFinding

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        scope = d.pop("scope")

        target_deployment_id = UUID(d.pop("target_deployment_id"))

        current_deployment_id = UUID(d.pop("current_deployment_id"))

        status = check_rollback_operation_status(d.pop("status"))

        service = d.pop("service")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        reason = d.pop("reason", UNSET)

        code = d.pop("code", UNSET)

        _blockers = d.pop("blockers", UNSET)
        blockers: list[BindingCheckFinding] | Unset = UNSET
        if _blockers is not UNSET:
            blockers = []
            for blockers_item_data in _blockers:
                blockers_item = BindingCheckFinding.from_dict(blockers_item_data)

                blockers.append(blockers_item)

        def _parse_completed_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                completed_at_type_0 = datetime.datetime.fromisoformat(data)

                return completed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        completed_at = _parse_completed_at(d.pop("completed_at", UNSET))

        audit_id = d.pop("audit_id", UNSET)

        rollback_operation = cls(
            id=id,
            app_id=app_id,
            scope=scope,
            target_deployment_id=target_deployment_id,
            current_deployment_id=current_deployment_id,
            status=status,
            service=service,
            created_at=created_at,
            updated_at=updated_at,
            reason=reason,
            code=code,
            blockers=blockers,
            completed_at=completed_at,
            audit_id=audit_id,
        )

        rollback_operation.additional_properties = d
        return rollback_operation

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
