from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.managed_realtime_reducer_response_entities import ManagedRealtimeReducerResponseEntities
    from ..models.managed_realtime_reducer_response_entity_expirations import (
        ManagedRealtimeReducerResponseEntityExpirations,
    )
    from ..models.managed_realtime_reducer_response_entity_versions import ManagedRealtimeReducerResponseEntityVersions


T = TypeVar("T", bound="ManagedRealtimeReducerResponse")


@_attrs_define
class ManagedRealtimeReducerResponse:
    """Configured channel state reducer and its current version."""

    channel: str
    sequence: int
    entities: ManagedRealtimeReducerResponseEntities
    entity_versions: ManagedRealtimeReducerResponseEntityVersions
    """Entity versions, including deletion tombstones."""
    updated_at: datetime.datetime
    entity_expirations: ManagedRealtimeReducerResponseEntityExpirations | Unset = UNSET
    """Scheduled entity deadlines; cleanup is asynchronous."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        channel = self.channel

        sequence = self.sequence

        entities = self.entities.to_dict()

        entity_versions = self.entity_versions.to_dict()

        updated_at = self.updated_at.isoformat()

        entity_expirations: dict[str, Any] | Unset = UNSET
        if not isinstance(self.entity_expirations, Unset):
            entity_expirations = self.entity_expirations.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "channel": channel,
                "sequence": sequence,
                "entities": entities,
                "entity_versions": entity_versions,
                "updated_at": updated_at,
            }
        )
        if entity_expirations is not UNSET:
            field_dict["entity_expirations"] = entity_expirations

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_realtime_reducer_response_entities import ManagedRealtimeReducerResponseEntities
        from ..models.managed_realtime_reducer_response_entity_expirations import (
            ManagedRealtimeReducerResponseEntityExpirations,
        )
        from ..models.managed_realtime_reducer_response_entity_versions import (
            ManagedRealtimeReducerResponseEntityVersions,
        )

        d = dict(src_dict)
        channel = d.pop("channel")

        sequence = d.pop("sequence")

        entities = ManagedRealtimeReducerResponseEntities.from_dict(d.pop("entities"))

        entity_versions = ManagedRealtimeReducerResponseEntityVersions.from_dict(d.pop("entity_versions"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _entity_expirations = d.pop("entity_expirations", UNSET)
        entity_expirations: ManagedRealtimeReducerResponseEntityExpirations | Unset
        if isinstance(_entity_expirations, Unset):
            entity_expirations = UNSET
        else:
            entity_expirations = ManagedRealtimeReducerResponseEntityExpirations.from_dict(_entity_expirations)

        managed_realtime_reducer_response = cls(
            channel=channel,
            sequence=sequence,
            entities=entities,
            entity_versions=entity_versions,
            updated_at=updated_at,
            entity_expirations=entity_expirations,
        )

        managed_realtime_reducer_response.additional_properties = d
        return managed_realtime_reducer_response

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
