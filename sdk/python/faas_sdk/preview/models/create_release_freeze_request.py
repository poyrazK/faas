from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_release_freeze_request_kind import (
    CreateReleaseFreezeRequestKind,
    check_create_release_freeze_request_kind,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.release_freeze_weekly import ReleaseFreezeWeekly


T = TypeVar("T", bound="CreateReleaseFreezeRequest")


@_attrs_define
class CreateReleaseFreezeRequest:
    """Creates a freeze. A window takes `starts_at` (default now) and optional `ends_at`; a weekly freeze takes `weekly`;
    an after-rollback policy takes `after_rollback_minutes`.

    """

    kind: CreateReleaseFreezeRequestKind
    reason: str
    app: str | Unset = UNSET
    """App slug; omit to freeze every app of the account."""
    starts_at: datetime.datetime | Unset = UNSET
    """Window start; default now."""
    ends_at: datetime.datetime | Unset = UNSET
    """Window end; omit to freeze until lifted."""
    weekly: ReleaseFreezeWeekly | Unset = UNSET
    """A weekly freeze window: it opens on each listed day at `start` in `time_zone` and lasts `duration_minutes`,
    so it may run past midnight or into the next week."""
    after_rollback_minutes: int | Unset = UNSET
    """Freeze length after each automatic rollback."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        reason = self.reason

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

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "reason": reason,
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

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.release_freeze_weekly import ReleaseFreezeWeekly

        d = dict(src_dict)
        kind = check_create_release_freeze_request_kind(d.pop("kind"))

        reason = d.pop("reason")

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

        create_release_freeze_request = cls(
            kind=kind,
            reason=reason,
            app=app,
            starts_at=starts_at,
            ends_at=ends_at,
            weekly=weekly,
            after_rollback_minutes=after_rollback_minutes,
        )

        create_release_freeze_request.additional_properties = d
        return create_release_freeze_request

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
