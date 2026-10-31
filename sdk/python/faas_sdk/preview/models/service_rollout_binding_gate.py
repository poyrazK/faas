from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.service_rollout_binding_gate_action import (
    ServiceRolloutBindingGateAction,
    check_service_rollout_binding_gate_action,
)
from ..models.service_rollout_binding_gate_status import (
    ServiceRolloutBindingGateStatus,
    check_service_rollout_binding_gate_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.binding_check_finding import BindingCheckFinding


T = TypeVar("T", bound="ServiceRolloutBindingGate")


@_attrs_define
class ServiceRolloutBindingGate:
    """Bounded progress of an exact binding check at the service routing boundary. Passed confirms the routing transaction;
    scheduler ACK and drain completion are reported by the enclosing handoff phase.

    """

    request_id: UUID
    action: ServiceRolloutBindingGateAction
    deployment_id: UUID
    status: ServiceRolloutBindingGateStatus
    code: str | Unset = UNSET
    blockers: list[BindingCheckFinding] | Unset = UNSET
    checked_at: datetime.datetime | None | Unset = UNSET
    policy_revision: int | Unset = UNSET
    audit_id: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        request_id = str(self.request_id)

        action: str = self.action

        deployment_id = str(self.deployment_id)

        status: str = self.status

        code = self.code

        blockers: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.blockers, Unset):
            blockers = []
            for blockers_item_data in self.blockers:
                blockers_item = blockers_item_data.to_dict()
                blockers.append(blockers_item)

        checked_at: None | str | Unset
        if isinstance(self.checked_at, Unset):
            checked_at = UNSET
        elif isinstance(self.checked_at, datetime.datetime):
            checked_at = self.checked_at.isoformat()
        else:
            checked_at = self.checked_at

        policy_revision = self.policy_revision

        audit_id = self.audit_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "request_id": request_id,
                "action": action,
                "deployment_id": deployment_id,
                "status": status,
            }
        )
        if code is not UNSET:
            field_dict["code"] = code
        if blockers is not UNSET:
            field_dict["blockers"] = blockers
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at
        if policy_revision is not UNSET:
            field_dict["policy_revision"] = policy_revision
        if audit_id is not UNSET:
            field_dict["audit_id"] = audit_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.binding_check_finding import BindingCheckFinding

        d = dict(src_dict)
        request_id = UUID(d.pop("request_id"))

        action = check_service_rollout_binding_gate_action(d.pop("action"))

        deployment_id = UUID(d.pop("deployment_id"))

        status = check_service_rollout_binding_gate_status(d.pop("status"))

        code = d.pop("code", UNSET)

        _blockers = d.pop("blockers", UNSET)
        blockers: list[BindingCheckFinding] | Unset = UNSET
        if _blockers is not UNSET:
            blockers = []
            for blockers_item_data in _blockers:
                blockers_item = BindingCheckFinding.from_dict(blockers_item_data)

                blockers.append(blockers_item)

        def _parse_checked_at(data: object) -> datetime.datetime | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                checked_at_type_0 = datetime.datetime.fromisoformat(data)

                return checked_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None | Unset, data)

        checked_at = _parse_checked_at(d.pop("checked_at", UNSET))

        policy_revision = d.pop("policy_revision", UNSET)

        audit_id = d.pop("audit_id", UNSET)

        service_rollout_binding_gate = cls(
            request_id=request_id,
            action=action,
            deployment_id=deployment_id,
            status=status,
            code=code,
            blockers=blockers,
            checked_at=checked_at,
            policy_revision=policy_revision,
            audit_id=audit_id,
        )

        service_rollout_binding_gate.additional_properties = d
        return service_rollout_binding_gate

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
