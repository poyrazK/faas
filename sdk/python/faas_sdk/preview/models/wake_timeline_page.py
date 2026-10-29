from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.wake_timeline_page_mode import WakeTimelinePageMode, check_wake_timeline_page_mode
from ..types import UNSET, Unset

T = TypeVar("T", bound="WakeTimelinePage")


@_attrs_define
class WakeTimelinePage:
    """Opt-in keyset_v1 metadata. App scoping and canonical boot selection precede the exclusive timestamp/event-ID seek
    and page limit. Complete means no further retained row in this live read, not complete capture or a frozen snapshot.
    Native event IDs stay inside the opaque cursor.

    """

    mode: WakeTimelinePageMode
    complete: bool
    since: datetime.datetime | Unset = UNSET
    """Normalized original exclusive lower bound; omitted when no lower bound was supplied."""
    cursor: str | Unset = UNSET
    """Echo of the supplied opaque page cursor."""
    next_cursor: str | Unset = UNSET
    """Opaque continuation for the same account/app/wake/since/limit. Omitted on terminal retained pages."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        complete = self.complete

        since: str | Unset = UNSET
        if not isinstance(self.since, Unset):
            since = self.since.isoformat()

        cursor = self.cursor

        next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "mode": mode,
                "complete": complete,
            }
        )
        if since is not UNSET:
            field_dict["since"] = since
        if cursor is not UNSET:
            field_dict["cursor"] = cursor
        if next_cursor is not UNSET:
            field_dict["next_cursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_wake_timeline_page_mode(d.pop("mode"))

        complete = d.pop("complete")

        _since = d.pop("since", UNSET)
        since: datetime.datetime | Unset
        if isinstance(_since, Unset):
            since = UNSET
        else:
            since = datetime.datetime.fromisoformat(_since)

        cursor = d.pop("cursor", UNSET)

        next_cursor = d.pop("next_cursor", UNSET)

        wake_timeline_page = cls(
            mode=mode,
            complete=complete,
            since=since,
            cursor=cursor,
            next_cursor=next_cursor,
        )

        wake_timeline_page.additional_properties = d
        return wake_timeline_page

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
