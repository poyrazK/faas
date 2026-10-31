from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.debug_running_cause import DebugRunningCause
    from ..models.debug_running_config import DebugRunningConfig
    from ..models.debug_running_observation import DebugRunningObservation


T = TypeVar("T", bound="DebugRunningResponse")


@_attrs_define
class DebugRunningResponse:
    """Customer-safe explanation of why an app remained resident. `current`
    is the newest observation and `history` contains the bounded recent
    observations in the requested window.

    """

    app_id: UUID
    since: str
    window_start: datetime.datetime
    window_end: datetime.datetime
    retention_clamped: bool
    current: list[DebugRunningCause]
    config: DebugRunningConfig
    """Configuration context shown with the observed running causes."""
    history: list[DebugRunningObservation]
    history_truncated: bool
    current_observed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        since = self.since

        window_start = self.window_start.isoformat()

        window_end = self.window_end.isoformat()

        retention_clamped = self.retention_clamped

        current = []
        for current_item_data in self.current:
            current_item = current_item_data.to_dict()
            current.append(current_item)

        config = self.config.to_dict()

        history = []
        for history_item_data in self.history:
            history_item = history_item_data.to_dict()
            history.append(history_item)

        history_truncated = self.history_truncated

        current_observed_at: str | Unset = UNSET
        if not isinstance(self.current_observed_at, Unset):
            current_observed_at = self.current_observed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "since": since,
                "window_start": window_start,
                "window_end": window_end,
                "retention_clamped": retention_clamped,
                "current": current,
                "config": config,
                "history": history,
                "history_truncated": history_truncated,
            }
        )
        if current_observed_at is not UNSET:
            field_dict["current_observed_at"] = current_observed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.debug_running_cause import DebugRunningCause
        from ..models.debug_running_config import DebugRunningConfig
        from ..models.debug_running_observation import DebugRunningObservation

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        since = d.pop("since")

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        window_end = datetime.datetime.fromisoformat(d.pop("window_end"))

        retention_clamped = d.pop("retention_clamped")

        current = []
        _current = d.pop("current")
        for current_item_data in _current:
            current_item = DebugRunningCause.from_dict(current_item_data)

            current.append(current_item)

        config = DebugRunningConfig.from_dict(d.pop("config"))

        history = []
        _history = d.pop("history")
        for history_item_data in _history:
            history_item = DebugRunningObservation.from_dict(history_item_data)

            history.append(history_item)

        history_truncated = d.pop("history_truncated")

        _current_observed_at = d.pop("current_observed_at", UNSET)
        current_observed_at: datetime.datetime | Unset
        if isinstance(_current_observed_at, Unset):
            current_observed_at = UNSET
        else:
            current_observed_at = datetime.datetime.fromisoformat(_current_observed_at)

        debug_running_response = cls(
            app_id=app_id,
            since=since,
            window_start=window_start,
            window_end=window_end,
            retention_clamped=retention_clamped,
            current=current,
            config=config,
            history=history,
            history_truncated=history_truncated,
            current_observed_at=current_observed_at,
        )

        debug_running_response.additional_properties = d
        return debug_running_response

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
