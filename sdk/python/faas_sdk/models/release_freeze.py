from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.release_freeze_kind import ReleaseFreezeKind, check_release_freeze_kind
from ..models.release_freeze_source import ReleaseFreezeSource, check_release_freeze_source
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.release_freeze_weekly import ReleaseFreezeWeekly


T = TypeVar("T", bound="ReleaseFreeze")


@_attrs_define
class ReleaseFreeze:
    """A deploy freeze on production releases of one app or, without `app`, of every app of the account."""

    id: UUID
    kind: ReleaseFreezeKind
    reason: str
    source: ReleaseFreezeSource
    """automatic_rollback for the window an after-rollback policy created."""
    created_by: str
    created_at: datetime.datetime
    active: bool
    """Whether the freeze withholds production releases now."""
    app: str | Unset = UNSET
    """The covered app's slug; omitted for an account-wide freeze."""
    starts_at: datetime.datetime | Unset = UNSET
    """Window start."""
    ends_at: datetime.datetime | Unset = UNSET
    """Window end; omitted for a window that lasts until lifted."""
    weekly: ReleaseFreezeWeekly | Unset = UNSET
    """A weekly freeze window: it opens on each listed day at `start` in `time_zone` and lasts `duration_minutes`,
    so it may run past midnight or into the next week."""
    after_rollback_minutes: int | Unset = UNSET
    """How long an after-rollback policy freezes an app Gregale rolled back automatically."""
    source_deployment_id: UUID | Unset = UNSET
    """The rolled-back deployment behind an automatic freeze."""
    next_starts_at: datetime.datetime | Unset = UNSET
    """When an inactive window or weekly freeze next starts, within a week."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        kind: str = self.kind

        reason = self.reason

        source: str = self.source

        created_by = self.created_by

        created_at = self.created_at.isoformat()

        active = self.active

        app = self.app

        starts_at: str | Unset = UNSET
        if not isinstance(self.starts_at, Unset):
            starts_at = self.starts_at.isoformat()

        ends_at: str | Unset = UNSET
        if not isinstance(self.ends_at, Unset):
            ends_at = self.ends_at.isoformat()

        weekly: dict[str, Any] | Unset = UNSET
        if not isinstance(self.weekly, Unset):
            weekly = self.weekly.to_dict()

        after_rollback_minutes = self.after_rollback_minutes

        source_deployment_id: str | Unset = UNSET
        if not isinstance(self.source_deployment_id, Unset):
            source_deployment_id = str(self.source_deployment_id)

        next_starts_at: str | Unset = UNSET
        if not isinstance(self.next_starts_at, Unset):
            next_starts_at = self.next_starts_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "kind": kind,
                "reason": reason,
                "source": source,
                "created_by": created_by,
                "created_at": created_at,
                "active": active,
            }
        )
        if app is not UNSET:
            field_dict["app"] = app
        if starts_at is not UNSET:
            field_dict["starts_at"] = starts_at
        if ends_at is not UNSET:
            field_dict["ends_at"] = ends_at
        if weekly is not UNSET:
            field_dict["weekly"] = weekly
        if after_rollback_minutes is not UNSET:
            field_dict["after_rollback_minutes"] = after_rollback_minutes
        if source_deployment_id is not UNSET:
            field_dict["source_deployment_id"] = source_deployment_id
        if next_starts_at is not UNSET:
            field_dict["next_starts_at"] = next_starts_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.release_freeze_weekly import ReleaseFreezeWeekly

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        kind = check_release_freeze_kind(d.pop("kind"))

        reason = d.pop("reason")

        source = check_release_freeze_source(d.pop("source"))

        created_by = d.pop("created_by")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        active = d.pop("active")

        app = d.pop("app", UNSET)

        _starts_at = d.pop("starts_at", UNSET)
        starts_at: datetime.datetime | Unset
        if isinstance(_starts_at, Unset):
            starts_at = UNSET
        else:
            starts_at = datetime.datetime.fromisoformat(_starts_at)

        _ends_at = d.pop("ends_at", UNSET)
        ends_at: datetime.datetime | Unset
        if isinstance(_ends_at, Unset):
            ends_at = UNSET
        else:
            ends_at = datetime.datetime.fromisoformat(_ends_at)

        _weekly = d.pop("weekly", UNSET)
        weekly: ReleaseFreezeWeekly | Unset
        if isinstance(_weekly, Unset):
            weekly = UNSET
        else:
            weekly = ReleaseFreezeWeekly.from_dict(_weekly)

        after_rollback_minutes = d.pop("after_rollback_minutes", UNSET)

        _source_deployment_id = d.pop("source_deployment_id", UNSET)
        source_deployment_id: UUID | Unset
        if isinstance(_source_deployment_id, Unset):
            source_deployment_id = UNSET
        else:
            source_deployment_id = UUID(_source_deployment_id)

        _next_starts_at = d.pop("next_starts_at", UNSET)
        next_starts_at: datetime.datetime | Unset
        if isinstance(_next_starts_at, Unset):
            next_starts_at = UNSET
        else:
            next_starts_at = datetime.datetime.fromisoformat(_next_starts_at)

        release_freeze = cls(
            id=id,
            kind=kind,
            reason=reason,
            source=source,
            created_by=created_by,
            created_at=created_at,
            active=active,
            app=app,
            starts_at=starts_at,
            ends_at=ends_at,
            weekly=weekly,
            after_rollback_minutes=after_rollback_minutes,
            source_deployment_id=source_deployment_id,
            next_starts_at=next_starts_at,
        )

        release_freeze.additional_properties = d
        return release_freeze

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
