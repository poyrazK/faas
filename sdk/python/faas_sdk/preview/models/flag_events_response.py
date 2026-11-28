from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.flag_events_response_dropped import FlagEventsResponseDropped
    from ..models.flag_events_response_month import FlagEventsResponseMonth


T = TypeVar("T", bound="FlagEventsResponse")


@_attrs_define
class FlagEventsResponse:
    """Accepted and dropped events of a batch, with the month's event usage."""

    accepted: int
    dropped: FlagEventsResponseDropped
    month: FlagEventsResponseMonth
    exposures: int | Unset = UNSET
    """Exposures that matched a running subject experiment of an active customer."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        accepted = self.accepted

        dropped = self.dropped.to_dict()

        month = self.month.to_dict()

        exposures = self.exposures

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "accepted": accepted,
                "dropped": dropped,
                "month": month,
            }
        )
        if exposures is not UNSET:
            field_dict["exposures"] = exposures

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.flag_events_response_dropped import FlagEventsResponseDropped
        from ..models.flag_events_response_month import FlagEventsResponseMonth

        d = dict(src_dict)
        accepted = d.pop("accepted")

        dropped = FlagEventsResponseDropped.from_dict(d.pop("dropped"))

        month = FlagEventsResponseMonth.from_dict(d.pop("month"))

        exposures = d.pop("exposures", UNSET)

        flag_events_response = cls(
            accepted=accepted,
            dropped=dropped,
            month=month,
            exposures=exposures,
        )

        flag_events_response.additional_properties = d
        return flag_events_response

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
