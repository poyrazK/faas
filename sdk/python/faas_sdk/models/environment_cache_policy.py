from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.environment_cache_rule import EnvironmentCacheRule


T = TypeVar("T", bound="EnvironmentCachePolicy")


@_attrs_define
class EnvironmentCachePolicy:
    """Desired cache mode and monotonic revision for one named environment; absence preserves app inheritance at revision
    zero.

    """

    app_id: UUID
    project_id: UUID
    environment_id: UUID
    scope: str
    revision: int
    inherited: bool
    """True at revision zero or a positive restored-inheritance revision; inherited app rule bodies are not
    returned."""
    rules: list[EnvironmentCacheRule]
    updated_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        project_id = str(self.project_id)

        environment_id = str(self.environment_id)

        scope = self.scope

        revision = self.revision

        inherited = self.inherited

        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "project_id": project_id,
                "environment_id": environment_id,
                "scope": scope,
                "revision": revision,
                "inherited": inherited,
                "rules": rules,
            }
        )
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_cache_rule import EnvironmentCacheRule

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        project_id = UUID(d.pop("project_id"))

        environment_id = UUID(d.pop("environment_id"))

        scope = d.pop("scope")

        revision = d.pop("revision")

        inherited = d.pop("inherited")

        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = EnvironmentCacheRule.from_dict(rules_item_data)

            rules.append(rules_item)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        environment_cache_policy = cls(
            app_id=app_id,
            project_id=project_id,
            environment_id=environment_id,
            scope=scope,
            revision=revision,
            inherited=inherited,
            rules=rules,
            updated_at=updated_at,
        )

        return environment_cache_policy
