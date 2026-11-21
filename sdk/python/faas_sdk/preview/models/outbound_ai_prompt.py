from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.outbound_ai_prompt_canary import OutboundAIPromptCanary


T = TypeVar("T", bound="OutboundAIPrompt")


@_attrs_define
class OutboundAIPrompt:
    """A managed AI prompt. live_version serves unpinned calls; previous_live_version is what a rollback restores."""

    id: UUID
    name: str
    description: str
    live_version: int | None
    previous_live_version: int | None
    latest_version: int
    canary: None | OutboundAIPromptCanary
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.outbound_ai_prompt_canary import OutboundAIPromptCanary

        id = str(self.id)

        name = self.name

        description = self.description

        live_version: int | None
        live_version = self.live_version

        previous_live_version: int | None
        previous_live_version = self.previous_live_version

        latest_version = self.latest_version

        canary: dict[str, Any] | None
        if isinstance(self.canary, OutboundAIPromptCanary):
            canary = self.canary.to_dict()
        else:
            canary = self.canary

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "description": description,
                "live_version": live_version,
                "previous_live_version": previous_live_version,
                "latest_version": latest_version,
                "canary": canary,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_prompt_canary import OutboundAIPromptCanary

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        description = d.pop("description")

        def _parse_live_version(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        live_version = _parse_live_version(d.pop("live_version"))

        def _parse_previous_live_version(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        previous_live_version = _parse_previous_live_version(d.pop("previous_live_version"))

        latest_version = d.pop("latest_version")

        def _parse_canary(data: object) -> None | OutboundAIPromptCanary:
            if data is None:
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                canary_type_0 = OutboundAIPromptCanary.from_dict(data)

                return canary_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | OutboundAIPromptCanary, data)

        canary = _parse_canary(d.pop("canary"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        outbound_ai_prompt = cls(
            id=id,
            name=name,
            description=description,
            live_version=live_version,
            previous_live_version=previous_live_version,
            latest_version=latest_version,
            canary=canary,
            created_at=created_at,
            updated_at=updated_at,
        )

        outbound_ai_prompt.additional_properties = d
        return outbound_ai_prompt

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
