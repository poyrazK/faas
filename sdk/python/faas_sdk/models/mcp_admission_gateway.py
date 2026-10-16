from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="MCPAdmissionGateway")


@_attrs_define
class MCPAdmissionGateway:
    """Gateway replay metadata captured with a retained traffic admission decision."""

    node_name: str
    boot_id: None | UUID
    revision: int | None
    observed_at: datetime.datetime | None
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        node_name = self.node_name

        boot_id: None | str
        if isinstance(self.boot_id, UUID):
            boot_id = str(self.boot_id)
        else:
            boot_id = self.boot_id

        revision: int | None
        revision = self.revision

        observed_at: None | str
        if isinstance(self.observed_at, datetime.datetime):
            observed_at = self.observed_at.isoformat()
        else:
            observed_at = self.observed_at

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "node_name": node_name,
                "boot_id": boot_id,
                "revision": revision,
                "observed_at": observed_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        node_name = d.pop("node_name")

        def _parse_boot_id(data: object) -> None | UUID:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                boot_id_type_0 = UUID(data)

                return boot_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | UUID, data)

        boot_id = _parse_boot_id(d.pop("boot_id"))

        def _parse_revision(data: object) -> int | None:
            if data is None:
                return data
            return cast(int | None, data)

        revision = _parse_revision(d.pop("revision"))

        def _parse_observed_at(data: object) -> datetime.datetime | None:
            if data is None:
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                observed_at_type_0 = datetime.datetime.fromisoformat(data)

                return observed_at_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(datetime.datetime | None, data)

        observed_at = _parse_observed_at(d.pop("observed_at"))

        mcp_admission_gateway = cls(
            node_name=node_name,
            boot_id=boot_id,
            revision=revision,
            observed_at=observed_at,
        )

        mcp_admission_gateway.additional_properties = d
        return mcp_admission_gateway

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
