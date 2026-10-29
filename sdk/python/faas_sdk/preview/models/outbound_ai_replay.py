from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_ai_replay_state import OutboundAIReplayState, check_outbound_ai_replay_state

if TYPE_CHECKING:
    from ..models.outbound_ai_replay_result import OutboundAIReplayResult
    from ..models.outbound_ai_replay_source import OutboundAIReplaySource
    from ..models.outbound_ai_replay_target import OutboundAIReplayTarget


T = TypeVar("T", bound="OutboundAIReplay")


@_attrs_define
class OutboundAIReplay:
    """A replay of one captured call and, once done, its results in target order."""

    id: UUID
    integration_id: UUID
    request_id: UUID
    state: OutboundAIReplayState
    error_code: None | str
    created_at: datetime.datetime
    finished_at: datetime.datetime | None
    targets: list[OutboundAIReplayTarget]
    source: OutboundAIReplaySource
    """The captured call a replay resends."""
    results: list[OutboundAIReplayResult]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        integration_id = str(self.integration_id)

        request_id = str(self.request_id)

        state: str = self.state

        error_code: None | str
        error_code = self.error_code

        created_at = self.created_at.isoformat()

        finished_at: None | str
        if isinstance(self.finished_at, datetime.datetime):
            finished_at = self.finished_at.isoformat()
        else:
            finished_at = self.finished_at

        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        source = self.source.to_dict()

        results = []
        for results_item_data in self.results:
            results_item = results_item_data.to_dict()
            results.append(results_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "integration_id": integration_id,
                "request_id": request_id,
                "state": state,
                "error_code": error_code,
                "created_at": created_at,
                "finished_at": finished_at,
                "targets": targets,
                "source": source,
                "results": results,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_replay_result import OutboundAIReplayResult
        from ..models.outbound_ai_replay_source import OutboundAIReplaySource
        from ..models.outbound_ai_replay_target import OutboundAIReplayTarget

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        integration_id = UUID(d.pop("integration_id"))

        request_id = UUID(d.pop("request_id"))

        state = check_outbound_ai_replay_state(d.pop("state"))

        def _parse_error_code(data: object) -> None | str:
            if data is None:
                return data
            return cast(None | str, data)

        error_code = _parse_error_code(d.pop("error_code"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        def _parse_finished_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                finished_at_type_0 = datetime.datetime.fromisoformat(data)

                return finished_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        finished_at = _parse_finished_at(d.pop("finished_at"))

        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = OutboundAIReplayTarget.from_dict(targets_item_data)

            targets.append(targets_item)

        source = OutboundAIReplaySource.from_dict(d.pop("source"))

        results = []
        _results = d.pop("results")
        for results_item_data in _results:
            results_item = OutboundAIReplayResult.from_dict(results_item_data)

            results.append(results_item)

        outbound_ai_replay = cls(
            id=id,
            integration_id=integration_id,
            request_id=request_id,
            state=state,
            error_code=error_code,
            created_at=created_at,
            finished_at=finished_at,
            targets=targets,
            source=source,
            results=results,
        )

        outbound_ai_replay.additional_properties = d
        return outbound_ai_replay

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
