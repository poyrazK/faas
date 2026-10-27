from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_pipeline_capacity_state import DataPipelineCapacityState, check_data_pipeline_capacity_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_endpoint_capacity import DataEndpointCapacity


T = TypeVar("T", bound="DataPipelineCapacity")


@_attrs_define
class DataPipelineCapacity:
    """New-allocation readiness at observation time. Admission atomically rechecks and reserves capacity; observations
    alone reserve nothing.

    """

    state: DataPipelineCapacityState
    source: DataEndpointCapacity | Unset = UNSET
    """Credential-free shared physical allocation and initial-copy peak demand. Other Gregale reservations remain
    counted while paused or awaiting cleanup."""
    destination: DataEndpointCapacity | Unset = UNSET
    """Credential-free shared physical allocation and initial-copy peak demand. Other Gregale reservations remain
    counted while paused or awaiting cleanup."""

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        source: dict[str, Any] | Unset = UNSET
        if not isinstance(self.source, Unset):
            source = self.source.to_dict()

        destination: dict[str, Any] | Unset = UNSET
        if not isinstance(self.destination, Unset):
            destination = self.destination.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "state": state,
            }
        )
        if source is not UNSET:
            field_dict["source"] = source
        if destination is not UNSET:
            field_dict["destination"] = destination

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_endpoint_capacity import DataEndpointCapacity

        d = dict(src_dict)
        state = check_data_pipeline_capacity_state(d.pop("state"))

        _source = d.pop("source", UNSET)
        source: DataEndpointCapacity | Unset
        if isinstance(_source, Unset):
            source = UNSET
        else:
            source = DataEndpointCapacity.from_dict(_source)

        _destination = d.pop("destination", UNSET)
        destination: DataEndpointCapacity | Unset
        if isinstance(_destination, Unset):
            destination = UNSET
        else:
            destination = DataEndpointCapacity.from_dict(_destination)

        data_pipeline_capacity = cls(
            state=state,
            source=source,
            destination=destination,
        )

        return data_pipeline_capacity
