from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_endpoint_capacity_limiting_resource import (
    DataEndpointCapacityLimitingResource,
    check_data_endpoint_capacity_limiting_resource,
)
from ..models.data_endpoint_capacity_state import DataEndpointCapacityState, check_data_endpoint_capacity_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="DataEndpointCapacity")


@_attrs_define
class DataEndpointCapacity:
    """Credential-free shared physical allocation and initial-copy peak demand. Other Gregale reservations remain counted
    while paused or awaiting cleanup.

    """

    state: DataEndpointCapacityState
    usable_connections: int
    reserved_connections: int
    required_connections: int
    limiting_resource: DataEndpointCapacityLimitingResource | Unset = UNSET
    observed_at: datetime.datetime | Unset = UNSET
    expires_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        state: str = self.state

        usable_connections = self.usable_connections

        reserved_connections = self.reserved_connections

        required_connections = self.required_connections

        limiting_resource: str | Unset = UNSET
        if not isinstance(self.limiting_resource, Unset):
            limiting_resource = self.limiting_resource

        observed_at: str | Unset = UNSET
        if not isinstance(self.observed_at, Unset):
            observed_at = self.observed_at.isoformat()

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "state": state,
                "usable_connections": usable_connections,
                "reserved_connections": reserved_connections,
                "required_connections": required_connections,
            }
        )
        if limiting_resource is not UNSET:
            field_dict["limiting_resource"] = limiting_resource
        if observed_at is not UNSET:
            field_dict["observed_at"] = observed_at
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = check_data_endpoint_capacity_state(d.pop("state"))

        usable_connections = d.pop("usable_connections")

        reserved_connections = d.pop("reserved_connections")

        required_connections = d.pop("required_connections")

        _limiting_resource = d.pop("limiting_resource", UNSET)
        limiting_resource: DataEndpointCapacityLimitingResource | Unset
        if isinstance(_limiting_resource, Unset):
            limiting_resource = UNSET
        else:
            limiting_resource = check_data_endpoint_capacity_limiting_resource(_limiting_resource)

        _observed_at = d.pop("observed_at", UNSET)
        observed_at: datetime.datetime | Unset
        if isinstance(_observed_at, Unset):
            observed_at = UNSET
        else:
            observed_at = datetime.datetime.fromisoformat(_observed_at)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        data_endpoint_capacity = cls(
            state=state,
            usable_connections=usable_connections,
            reserved_connections=reserved_connections,
            required_connections=required_connections,
            limiting_resource=limiting_resource,
            observed_at=observed_at,
            expires_at=expires_at,
        )

        return data_endpoint_capacity
