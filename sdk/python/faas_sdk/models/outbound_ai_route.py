from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_ai_route_format import OutboundAIRouteFormat, check_outbound_ai_route_format
from ..models.outbound_ai_route_strategy import OutboundAIRouteStrategy, check_outbound_ai_route_strategy
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.outbound_ai_route_canary import OutboundAIRouteCanary
    from ..models.outbound_ai_route_target import OutboundAIRouteTarget


T = TypeVar("T", bound="OutboundAIRoute")


@_attrs_define
class OutboundAIRoute:
    """An ordered list of managed AI integrations. Apps call $GREGALE_OUTBOUND_URL/r/{id}/{provider path} (ADR-990). An
    openai-format route accepts OpenAI chat completions and embeddings and translates them for each target's provider
    (ADR-991, ADR-995).

    """

    id: UUID
    name: str
    format_: OutboundAIRouteFormat
    targets: list[OutboundAIRouteTarget]
    created_at: datetime.datetime
    updated_at: datetime.datetime
    strategy: OutboundAIRouteStrategy
    """How a call picks its first target (ADR-1434)."""
    max_ttfb_ms: int | None | Unset = UNSET
    """A cost route's latency target, or null."""
    canary: None | OutboundAIRouteCanary | Unset = UNSET
    """The route's model canary, or null (ADR-1340)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        from ..models.outbound_ai_route_canary import OutboundAIRouteCanary

        id = str(self.id)

        name = self.name

        format_: str = self.format_

        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        strategy: str = self.strategy

        max_ttfb_ms: int | None | Unset
        if isinstance(self.max_ttfb_ms, Unset):
            max_ttfb_ms = UNSET
        else:
            max_ttfb_ms = self.max_ttfb_ms

        canary: dict[str, Any] | None | Unset
        if isinstance(self.canary, Unset):
            canary = UNSET
        elif isinstance(self.canary, OutboundAIRouteCanary):
            canary = self.canary.to_dict()
        else:
            canary = self.canary

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "format": format_,
                "targets": targets,
                "created_at": created_at,
                "updated_at": updated_at,
                "strategy": strategy,
            }
        )
        if max_ttfb_ms is not UNSET:
            field_dict["max_ttfb_ms"] = max_ttfb_ms
        if canary is not UNSET:
            field_dict["canary"] = canary

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_route_canary import OutboundAIRouteCanary
        from ..models.outbound_ai_route_target import OutboundAIRouteTarget

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        name = d.pop("name")

        format_ = check_outbound_ai_route_format(d.pop("format"))

        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = OutboundAIRouteTarget.from_dict(targets_item_data)

            targets.append(targets_item)

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        strategy = check_outbound_ai_route_strategy(d.pop("strategy"))

        def _parse_max_ttfb_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        max_ttfb_ms = _parse_max_ttfb_ms(d.pop("max_ttfb_ms", UNSET))

        def _parse_canary(data: object) -> None | OutboundAIRouteCanary | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                canary_type_0 = OutboundAIRouteCanary.from_dict(data)

                return canary_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | OutboundAIRouteCanary | Unset, data)

        canary = _parse_canary(d.pop("canary", UNSET))

        outbound_ai_route = cls(
            id=id,
            name=name,
            format_=format_,
            targets=targets,
            created_at=created_at,
            updated_at=updated_at,
            strategy=strategy,
            max_ttfb_ms=max_ttfb_ms,
            canary=canary,
        )

        outbound_ai_route.additional_properties = d
        return outbound_ai_route

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
