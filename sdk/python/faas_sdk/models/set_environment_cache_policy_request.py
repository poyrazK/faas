from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.environment_cache_policy_source import EnvironmentCachePolicySource
    from ..models.environment_cache_rule import EnvironmentCacheRule


T = TypeVar("T", bound="SetEnvironmentCachePolicyRequest")


@_attrs_define
class SetEnvironmentCachePolicyRequest:
    """Complete named-environment replacement with immutable identity, revision guard and transactional retry ID."""

    app_id: UUID
    project_id: UUID
    environment_id: UUID
    request_id: UUID
    """Stable native operation ID; keep this ID and body on retries."""
    expected_revision: int
    rules: list[EnvironmentCacheRule]
    """Complete replacement; [] disables environment caching unless inherited=true restores app rules. Omission or
    null is invalid."""
    inherited: bool | Unset = UNSET
    """Optional; true restores live app cache inheritance at a new revision and requires rules=[]. Omitted or false
    replaces the override."""
    copy_from: EnvironmentCachePolicySource | Unset = UNSET
    """An explicit cache override in another named environment of the same app and project, pinned for an atomic
    one-time copy."""
    restore_revision: int | Unset = UNSET
    """Optional historical revision to restore as a new revision. Rules and inherited mode must match retained
    history; zero restores live inheritance. Cannot combine with copy_from."""

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        project_id = str(self.project_id)

        environment_id = str(self.environment_id)

        request_id = str(self.request_id)

        expected_revision = self.expected_revision

        rules = []
        for rules_item_data in self.rules:
            rules_item = rules_item_data.to_dict()
            rules.append(rules_item)

        inherited = self.inherited

        copy_from: dict[str, Any] | Unset = UNSET
        if not isinstance(self.copy_from, Unset):
            copy_from = self.copy_from.to_dict()

        restore_revision = self.restore_revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "app_id": app_id,
                "project_id": project_id,
                "environment_id": environment_id,
                "request_id": request_id,
                "expected_revision": expected_revision,
                "rules": rules,
            }
        )
        if inherited is not UNSET:
            field_dict["inherited"] = inherited
        if copy_from is not UNSET:
            field_dict["copy_from"] = copy_from
        if restore_revision is not UNSET:
            field_dict["restore_revision"] = restore_revision

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.environment_cache_policy_source import EnvironmentCachePolicySource
        from ..models.environment_cache_rule import EnvironmentCacheRule

        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        project_id = UUID(d.pop("project_id"))

        environment_id = UUID(d.pop("environment_id"))

        request_id = UUID(d.pop("request_id"))

        expected_revision = d.pop("expected_revision")

        rules = []
        _rules = d.pop("rules")
        for rules_item_data in _rules:
            rules_item = EnvironmentCacheRule.from_dict(rules_item_data)

            rules.append(rules_item)

        inherited = d.pop("inherited", UNSET)

        _copy_from = d.pop("copy_from", UNSET)
        copy_from: EnvironmentCachePolicySource | Unset
        if isinstance(_copy_from, Unset):
            copy_from = UNSET
        else:
            copy_from = EnvironmentCachePolicySource.from_dict(_copy_from)

        restore_revision = d.pop("restore_revision", UNSET)

        set_environment_cache_policy_request = cls(
            app_id=app_id,
            project_id=project_id,
            environment_id=environment_id,
            request_id=request_id,
            expected_revision=expected_revision,
            rules=rules,
            inherited=inherited,
            copy_from=copy_from,
            restore_revision=restore_revision,
        )

        return set_environment_cache_policy_request
