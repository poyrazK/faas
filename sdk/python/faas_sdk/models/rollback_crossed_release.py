from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RollbackCrossedRelease")


@_attrs_define
class RollbackCrossedRelease:
    """A release command that ran after a rollback target last served (ADR-1448). A rollback does not undo it."""

    deployment_id: UUID
    status: str
    """The release task's status. A failed release may still have committed changes."""
    started_at: datetime.datetime
    revision: int | Unset = UNSET
    """The deployment's vN revision."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        deployment_id = str(self.deployment_id)

        status = self.status

        started_at = self.started_at.isoformat()

        revision = self.revision

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "deployment_id": deployment_id,
                "status": status,
                "started_at": started_at,
            }
        )
        if revision is not UNSET:
            field_dict["revision"] = revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        deployment_id = UUID(d.pop("deployment_id"))

        status = d.pop("status")

        started_at = datetime.datetime.fromisoformat(d.pop("started_at"))

        revision = d.pop("revision", UNSET)

        rollback_crossed_release = cls(
            deployment_id=deployment_id,
            status=status,
            started_at=started_at,
            revision=revision,
        )

        rollback_crossed_release.additional_properties = d
        return rollback_crossed_release

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
