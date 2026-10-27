from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.config_guard_status import ConfigGuardStatus, check_config_guard_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.config_guard_decision import ConfigGuardDecision


T = TypeVar("T", bound="ConfigGuard")


@_attrs_define
class ConfigGuard:
    """A scope's config guard."""

    id: UUID
    scope: str
    status: ConfigGuardStatus
    base_revision: int
    head_revision: int
    restorable: bool
    opened_at: datetime.datetime
    last_change_at: datetime.datetime
    window_ends_at: datetime.datetime | Unset = UNSET
    closed_at: datetime.datetime | Unset = UNSET
    decision: ConfigGuardDecision | Unset = UNSET
    """Why a config guard closed: the rule, a one-line summary and the new-config and old-config samples."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        scope = self.scope

        status: str = self.status

        base_revision = self.base_revision

        head_revision = self.head_revision

        restorable = self.restorable

        opened_at = self.opened_at.isoformat()

        last_change_at = self.last_change_at.isoformat()

        window_ends_at: str | Unset = UNSET
        if not isinstance(self.window_ends_at, Unset):
            window_ends_at = self.window_ends_at.isoformat()

        closed_at: str | Unset = UNSET
        if not isinstance(self.closed_at, Unset):
            closed_at = self.closed_at.isoformat()

        decision: dict[str, Any] | Unset = UNSET
        if not isinstance(self.decision, Unset):
            decision = self.decision.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "scope": scope,
                "status": status,
                "base_revision": base_revision,
                "head_revision": head_revision,
                "restorable": restorable,
                "opened_at": opened_at,
                "last_change_at": last_change_at,
            }
        )
        if window_ends_at is not UNSET:
            field_dict["window_ends_at"] = window_ends_at
        if closed_at is not UNSET:
            field_dict["closed_at"] = closed_at
        if decision is not UNSET:
            field_dict["decision"] = decision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.config_guard_decision import ConfigGuardDecision

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        scope = d.pop("scope")

        status = check_config_guard_status(d.pop("status"))

        base_revision = d.pop("base_revision")

        head_revision = d.pop("head_revision")

        restorable = d.pop("restorable")

        opened_at = datetime.datetime.fromisoformat(d.pop("opened_at"))

        last_change_at = datetime.datetime.fromisoformat(d.pop("last_change_at"))

        _window_ends_at = d.pop("window_ends_at", UNSET)
        window_ends_at: datetime.datetime | Unset
        if isinstance(_window_ends_at, Unset):
            window_ends_at = UNSET
        else:
            window_ends_at = datetime.datetime.fromisoformat(_window_ends_at)

        _closed_at = d.pop("closed_at", UNSET)
        closed_at: datetime.datetime | Unset
        if isinstance(_closed_at, Unset):
            closed_at = UNSET
        else:
            closed_at = datetime.datetime.fromisoformat(_closed_at)

        _decision = d.pop("decision", UNSET)
        decision: ConfigGuardDecision | Unset
        if isinstance(_decision, Unset):
            decision = UNSET
        else:
            decision = ConfigGuardDecision.from_dict(_decision)

        config_guard = cls(
            id=id,
            scope=scope,
            status=status,
            base_revision=base_revision,
            head_revision=head_revision,
            restorable=restorable,
            opened_at=opened_at,
            last_change_at=last_change_at,
            window_ends_at=window_ends_at,
            closed_at=closed_at,
            decision=decision,
        )

        config_guard.additional_properties = d
        return config_guard

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
