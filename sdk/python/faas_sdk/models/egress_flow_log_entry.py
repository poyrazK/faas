from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="EgressFlowLogEntry")


@_attrs_define
class EgressFlowLogEntry:
    """A destination address and TCP port a tenant guest opened a new flow to (ADR-371)."""

    observed_at: datetime.datetime
    node: str
    account_id: str
    app_id: str
    instance_id: str
    remote_ip: str
    remote_port: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        observed_at = self.observed_at.isoformat()

        node = self.node

        account_id = self.account_id

        app_id = self.app_id

        instance_id = self.instance_id

        remote_ip = self.remote_ip

        remote_port = self.remote_port

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "observed_at": observed_at,
                "node": node,
                "account_id": account_id,
                "app_id": app_id,
                "instance_id": instance_id,
                "remote_ip": remote_ip,
                "remote_port": remote_port,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        node = d.pop("node")

        account_id = d.pop("account_id")

        app_id = d.pop("app_id")

        instance_id = d.pop("instance_id")

        remote_ip = d.pop("remote_ip")

        remote_port = d.pop("remote_port")

        egress_flow_log_entry = cls(
            observed_at=observed_at,
            node=node,
            account_id=account_id,
            app_id=app_id,
            instance_id=instance_id,
            remote_ip=remote_ip,
            remote_port=remote_port,
        )

        egress_flow_log_entry.additional_properties = d
        return egress_flow_log_entry

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
