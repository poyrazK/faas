from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="EnvironmentCachePolicySource")


@_attrs_define
class EnvironmentCachePolicySource:
    """An explicit cache override in another named environment of the same app and project, pinned for an atomic one-time
    copy.

    """

    scope: str
    """Registered source environment slug; must differ from the destination."""
    environment_id: UUID
    revision: int

    def to_dict(self) -> dict[str, Any]:
        scope = self.scope

        environment_id = str(self.environment_id)

        revision = self.revision

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "scope": scope,
                "environment_id": environment_id,
                "revision": revision,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        scope = d.pop("scope")

        environment_id = UUID(d.pop("environment_id"))

        revision = d.pop("revision")

        environment_cache_policy_source = cls(
            scope=scope,
            environment_id=environment_id,
            revision=revision,
        )

        return environment_cache_policy_source
