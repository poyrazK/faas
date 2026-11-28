from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.alert_observation_skip_count import AlertObservationSkipCount


T = TypeVar("T", bound="AlertObservationCoverage")


@_attrs_define
class AlertObservationCoverage:
    """Recorded live Observe only checks during the 24 hours before this response."""

    attempted: int
    """ Recorded checks including skipped checks. """
    completed: int
    """ Checks that evaluated the condition. """
    skipped: list[AlertObservationSkipCount] | Unset = UNSET
    """ Recorded skips grouped by reason. """
    last_checked_at: datetime.datetime | Unset = UNSET
    """ Most recent recorded check for this observation even if older than 24 hours. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        attempted = self.attempted

        completed = self.completed

        skipped: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.skipped, Unset):
            skipped = []
            for skipped_item_data in self.skipped:
                skipped_item = skipped_item_data.to_dict()
                skipped.append(skipped_item)

        last_checked_at: str | Unset = UNSET
        if not isinstance(self.last_checked_at, Unset):
            last_checked_at = self.last_checked_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "attempted": attempted,
                "completed": completed,
            }
        )
        if skipped is not UNSET:
            field_dict["skipped"] = skipped
        if last_checked_at is not UNSET:
            field_dict["last_checked_at"] = last_checked_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.alert_observation_skip_count import AlertObservationSkipCount

        d = dict(src_dict)
        attempted = d.pop("attempted")

        completed = d.pop("completed")

        _skipped = d.pop("skipped", UNSET)
        skipped: list[AlertObservationSkipCount] | Unset = UNSET
        if _skipped is not UNSET:
            skipped = []
            for skipped_item_data in _skipped:
                skipped_item = AlertObservationSkipCount.from_dict(skipped_item_data)

                skipped.append(skipped_item)

        _last_checked_at = d.pop("last_checked_at", UNSET)
        last_checked_at: datetime.datetime | Unset
        if isinstance(_last_checked_at, Unset):
            last_checked_at = UNSET
        else:
            last_checked_at = datetime.datetime.fromisoformat(_last_checked_at)

        alert_observation_coverage = cls(
            attempted=attempted,
            completed=completed,
            skipped=skipped,
            last_checked_at=last_checked_at,
        )

        alert_observation_coverage.additional_properties = d
        return alert_observation_coverage

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
