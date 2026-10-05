from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.automation_response_source import AutomationResponseSource, check_automation_response_source
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.workflow_spec import WorkflowSpec


T = TypeVar("T", bound="AutomationResponse")


@_attrs_define
class AutomationResponse:
    """Saved draft, publication, ownership and opaque revision of one automation."""

    name: str
    version: int
    """Opaque monotonically increasing revision. Zero creates the first draft. A stale value returns
    automation_version_conflict."""
    source: AutomationResponseSource
    draft: WorkflowSpec
    """A named workflow DAG submitted with a deployment (ADR-081)."""
    enabled: bool
    published: WorkflowSpec | Unset = UNSET
    """A named workflow DAG submitted with a deployment (ADR-081)."""
    published_version: int | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        version = self.version

        source: str = self.source

        draft = self.draft.to_dict()

        enabled = self.enabled

        published: dict[str, Any] | Unset = UNSET
        if not isinstance(self.published, Unset):
            published = self.published.to_dict()

        published_version = self.published_version

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "version": version,
                "source": source,
                "draft": draft,
                "enabled": enabled,
            }
        )
        if published is not UNSET:
            field_dict["published"] = published
        if published_version is not UNSET:
            field_dict["published_version"] = published_version
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_spec import WorkflowSpec

        d = dict(src_dict)
        name = d.pop("name")

        version = d.pop("version")

        source = check_automation_response_source(d.pop("source"))

        draft = WorkflowSpec.from_dict(d.pop("draft"))

        enabled = d.pop("enabled")

        _published = d.pop("published", UNSET)
        published: WorkflowSpec | Unset
        if isinstance(_published, Unset):
            published = UNSET
        else:
            published = WorkflowSpec.from_dict(_published)

        published_version = d.pop("published_version", UNSET)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        automation_response = cls(
            name=name,
            version=version,
            source=source,
            draft=draft,
            enabled=enabled,
            published=published,
            published_version=published_version,
            updated_at=updated_at,
        )

        return automation_response
