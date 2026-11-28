from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.mcp_readiness_policy_change import MCPReadinessPolicyChange


T = TypeVar("T", bound="MCPReadinessPolicyDiff")


@_attrs_define
class MCPReadinessPolicyDiff:
    """Configuration differences between the selected readiness policy revisions."""

    scope: str
    from_revision: str
    to_revision: str
    changes: list[MCPReadinessPolicyChange]

    def to_dict(self) -> dict[str, Any]:
        scope = self.scope

        from_revision = self.from_revision

        to_revision = self.to_revision

        changes = []
        for changes_item_data in self.changes:
            changes_item = changes_item_data.to_dict()
            changes.append(changes_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "scope": scope,
                "from_revision": from_revision,
                "to_revision": to_revision,
                "changes": changes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.mcp_readiness_policy_change import MCPReadinessPolicyChange

        d = dict(src_dict)
        scope = d.pop("scope")

        from_revision = d.pop("from_revision")

        to_revision = d.pop("to_revision")

        changes = []
        _changes = d.pop("changes")
        for changes_item_data in _changes:
            changes_item = MCPReadinessPolicyChange.from_dict(changes_item_data)

            changes.append(changes_item)

        mcp_readiness_policy_diff = cls(
            scope=scope,
            from_revision=from_revision,
            to_revision=to_revision,
            changes=changes,
        )

        return mcp_readiness_policy_diff
