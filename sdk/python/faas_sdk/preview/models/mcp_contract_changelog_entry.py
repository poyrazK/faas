from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_contract_changelog_entry_verdict import (
    MCPContractChangelogEntryVerdict,
    check_mcp_contract_changelog_entry_verdict,
)

T = TypeVar("T", bound="MCPContractChangelogEntry")


@_attrs_define
class MCPContractChangelogEntry:
    """One tool contract change on a public listing: changed tool names, never schemas."""

    date: datetime.datetime
    verdict: MCPContractChangelogEntryVerdict
    tools: list[str]
    changes: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        date = self.date.isoformat()

        verdict: str = self.verdict

        tools = self.tools

        changes = self.changes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "date": date,
                "verdict": verdict,
                "tools": tools,
                "changes": changes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        date = datetime.datetime.fromisoformat(d.pop("date"))

        verdict = check_mcp_contract_changelog_entry_verdict(d.pop("verdict"))

        tools = cast(list[str], d.pop("tools"))

        changes = d.pop("changes")

        mcp_contract_changelog_entry = cls(
            date=date,
            verdict=verdict,
            tools=tools,
            changes=changes,
        )

        mcp_contract_changelog_entry.additional_properties = d
        return mcp_contract_changelog_entry

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
