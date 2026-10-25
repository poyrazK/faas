from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="RealtimeAcknowledgement")


@_attrs_define
class RealtimeAcknowledgement:
    """Realtime Acknowledgement."""

    checkpoint_sequence: str
    acknowledged_sequence: None | str
    """Latest advancing ACK; null after initialization or reset"""
    acknowledged_at: datetime.datetime | None
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        checkpoint_sequence = self.checkpoint_sequence

        acknowledged_sequence: None | str
        acknowledged_sequence = self.acknowledged_sequence

        acknowledged_at: None | str
        if isinstance(self.acknowledged_at, datetime.datetime):
            acknowledged_at = self.acknowledged_at.isoformat()
        else:
            acknowledged_at = self.acknowledged_at

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "checkpoint_sequence": checkpoint_sequence,
                "acknowledged_sequence": acknowledged_sequence,
                "acknowledged_at": acknowledged_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        checkpoint_sequence = d.pop("checkpoint_sequence")

        def _parse_acknowledged_sequence(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        acknowledged_sequence = _parse_acknowledged_sequence(d.pop("acknowledged_sequence"))

        def _parse_acknowledged_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                acknowledged_at_type_0 = datetime.datetime.fromisoformat(data)

                return acknowledged_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        acknowledged_at = _parse_acknowledged_at(d.pop("acknowledged_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        realtime_acknowledgement = cls(
            checkpoint_sequence=checkpoint_sequence,
            acknowledged_sequence=acknowledged_sequence,
            acknowledged_at=acknowledged_at,
            updated_at=updated_at,
        )

        realtime_acknowledgement.additional_properties = d
        return realtime_acknowledgement

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
