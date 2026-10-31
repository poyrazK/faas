from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="AgentClientRequest")


@_attrs_define
class AgentClientRequest:
    client_name: str
    """Printable client name, without control characters or surrounding spaces."""
    client_site: str | Unset = UNSET
    """Lower-case host name the client was verified at."""

    def to_dict(self) -> dict[str, Any]:
        client_name = self.client_name

        client_site = self.client_site

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "client_name": client_name,
            }
        )
        if client_site is not UNSET:
            field_dict["client_site"] = client_site

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        client_name = d.pop("client_name")

        client_site = d.pop("client_site", UNSET)

        agent_client_request = cls(
            client_name=client_name,
            client_site=client_site,
        )

        return agent_client_request
