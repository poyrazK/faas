from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.environment_cache_policy import EnvironmentCachePolicy


T = TypeVar("T", bound="EnvironmentCachePolicyHistory")


@_attrs_define
class EnvironmentCachePolicyHistory:
    """Bounded retained cache policy snapshots for one immutable named environment, ordered by descending revision."""

    app_id: UUID
    project_id: UUID
    environment_id: UUID
    scope: str
    current_revision: int
    entries: list[EnvironmentCachePolicy]
    next_before_revision: int | Unset = UNSET
    """Exclusive cursor for the next older page; omitted at the end."""

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        project_id = str(self.project_id)

        environment_id = str(self.environment_id)

        scope = self.scope

        current_revision = self.current_revision

        entries = []
        for entries_item_data in self.entries:
            entries_item = entries_item_data.to_dict()
            entries.append(entries_item)

        next_before_revision = self.next_before_revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "project_id": project_id,
                "environment_id": environment_id,
                "scope": scope,
                "current_revision": current_revision,
                "entries": entries,
            }
        )
        if next_before_revision is not UNSET:
            field_dict["next_before_revision"] = next_before_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_cache_policy import EnvironmentCachePolicy

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        project_id = UUID(d.pop("project_id"))

        environment_id = UUID(d.pop("environment_id"))

        scope = d.pop("scope")

        current_revision = d.pop("current_revision")

        entries = []
        _entries = d.pop("entries")
        for entries_item_data in _entries:
            entries_item = EnvironmentCachePolicy.from_dict(entries_item_data)

            entries.append(entries_item)

        next_before_revision = d.pop("next_before_revision", UNSET)

        environment_cache_policy_history = cls(
            app_id=app_id,
            project_id=project_id,
            environment_id=environment_id,
            scope=scope,
            current_revision=current_revision,
            entries=entries,
            next_before_revision=next_before_revision,
        )

        return environment_cache_policy_history
