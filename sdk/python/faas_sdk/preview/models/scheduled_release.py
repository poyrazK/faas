from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.scheduled_release_state import ScheduledReleaseState, check_scheduled_release_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.canary_preset_spec import CanaryPresetSpec


T = TypeVar("T", bound="ScheduledRelease")


@_attrs_define
class ScheduledRelease:
    """A release built ahead of time that starts its rollout at release_at."""

    id: UUID
    app: str
    deployment_id: UUID
    release_at: datetime.datetime
    state: ScheduledReleaseState
    notify_channels: list[str]
    scheduled_by: str
    created_at: datetime.datetime
    updated_at: datetime.datetime
    revision: int | Unset = UNSET
    canary: CanaryPresetSpec | Unset = UNSET
    """The canary ladder a customer asks for on a deploy (A + production-leveling
    Stream F). Preset is the catalog name (none/slow/balanced/aggressive/1-10-50-100/adaptive/
    custom). When Preset is 'custom', Stages is the
    customer-supplied ladder (each entry is percent +
    duration string in time.ParseDuration form, e.g.
    "1% at 30s, 10% at 2m, 100% at 0s").
    The wire-format change (StepDurations removed, Stages
    added) is additive on the consumer side because the
    prior StepDurations field was declared-but-dead — no
    pre-PR client ever sent it.
    """
    override_freeze: str | Unset = UNSET
    """The reason given for releasing into the freezes in overridden_freezes."""
    overridden_freezes: list[UUID] | Unset = UNSET
    outcome_code: str | Unset = UNSET
    """Why a schedule ended without starting, such as release_frozen or not_ready."""
    outcome_detail: str | Unset = UNSET
    started_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app = self.app

        deployment_id = str(self.deployment_id)

        release_at = self.release_at.isoformat()

        state: str = self.state

        notify_channels = self.notify_channels

        scheduled_by = self.scheduled_by

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        revision = self.revision

        canary: dict[str, Any] | Unset = UNSET
        if not isinstance(self.canary, Unset):
            canary = self.canary.to_dict()

        override_freeze = self.override_freeze

        overridden_freezes: list[str] | Unset = UNSET
        if not isinstance(self.overridden_freezes, Unset):
            overridden_freezes = []
            for overridden_freezes_item_data in self.overridden_freezes:
                overridden_freezes_item = str(overridden_freezes_item_data)
                overridden_freezes.append(overridden_freezes_item)

        outcome_code = self.outcome_code

        outcome_detail = self.outcome_detail

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app": app,
                "deployment_id": deployment_id,
                "release_at": release_at,
                "state": state,
                "notify_channels": notify_channels,
                "scheduled_by": scheduled_by,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if revision is not UNSET:
            field_dict["revision"] = revision
        if canary is not UNSET:
            field_dict["canary"] = canary
        if override_freeze is not UNSET:
            field_dict["override_freeze"] = override_freeze
        if overridden_freezes is not UNSET:
            field_dict["overridden_freezes"] = overridden_freezes
        if outcome_code is not UNSET:
            field_dict["outcome_code"] = outcome_code
        if outcome_detail is not UNSET:
            field_dict["outcome_detail"] = outcome_detail
        if started_at is not UNSET:
            field_dict["started_at"] = started_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.canary_preset_spec import CanaryPresetSpec

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app = d.pop("app")

        deployment_id = UUID(d.pop("deployment_id"))

        release_at = datetime.datetime.fromisoformat(d.pop("release_at"))

        state = check_scheduled_release_state(d.pop("state"))

        notify_channels = cast(list[str], d.pop("notify_channels"))

        scheduled_by = d.pop("scheduled_by")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        revision = d.pop("revision", UNSET)

        _canary = d.pop("canary", UNSET)
        canary: CanaryPresetSpec | Unset
        if isinstance(_canary, Unset):
            canary = UNSET
        else:
            canary = CanaryPresetSpec.from_dict(_canary)

        override_freeze = d.pop("override_freeze", UNSET)

        _overridden_freezes = d.pop("overridden_freezes", UNSET)
        overridden_freezes: list[UUID] | Unset = UNSET
        if _overridden_freezes is not UNSET:
            overridden_freezes = []
            for overridden_freezes_item_data in _overridden_freezes:
                overridden_freezes_item = UUID(overridden_freezes_item_data)

                overridden_freezes.append(overridden_freezes_item)

        outcome_code = d.pop("outcome_code", UNSET)

        outcome_detail = d.pop("outcome_detail", UNSET)

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        scheduled_release = cls(
            id=id,
            app=app,
            deployment_id=deployment_id,
            release_at=release_at,
            state=state,
            notify_channels=notify_channels,
            scheduled_by=scheduled_by,
            created_at=created_at,
            updated_at=updated_at,
            revision=revision,
            canary=canary,
            override_freeze=override_freeze,
            overridden_freezes=overridden_freezes,
            outcome_code=outcome_code,
            outcome_detail=outcome_detail,
            started_at=started_at,
        )

        scheduled_release.additional_properties = d
        return scheduled_release

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
