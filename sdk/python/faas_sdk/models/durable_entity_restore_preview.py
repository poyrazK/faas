from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.durable_entity_restore_preview_compatibility import (
    DurableEntityRestorePreviewCompatibility,
    check_durable_entity_restore_preview_compatibility,
)
from ..models.durable_entity_restore_preview_schema_relation import (
    DurableEntityRestorePreviewSchemaRelation,
    check_durable_entity_restore_preview_schema_relation,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DurableEntityRestorePreview")


@_attrs_define
class DurableEntityRestorePreview:
    current_version: int
    source_version: int
    expected_version_matches: bool
    schema_relation: DurableEntityRestorePreviewSchemaRelation
    compatibility: DurableEntityRestorePreviewCompatibility
    alarm_pending: bool
    outbox_pending: int
    alarm_exhausted: bool
    outbox_exhausted: bool
    current_schema_version: int | Unset = UNSET
    source_schema_version: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        current_version = self.current_version

        source_version = self.source_version

        expected_version_matches = self.expected_version_matches

        schema_relation: str = self.schema_relation

        compatibility: str = self.compatibility

        alarm_pending = self.alarm_pending

        outbox_pending = self.outbox_pending

        alarm_exhausted = self.alarm_exhausted

        outbox_exhausted = self.outbox_exhausted

        current_schema_version = self.current_schema_version

        source_schema_version = self.source_schema_version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "current_version": current_version,
                "source_version": source_version,
                "expected_version_matches": expected_version_matches,
                "schema_relation": schema_relation,
                "compatibility": compatibility,
                "alarm_pending": alarm_pending,
                "outbox_pending": outbox_pending,
                "alarm_exhausted": alarm_exhausted,
                "outbox_exhausted": outbox_exhausted,
            }
        )
        if current_schema_version is not UNSET:
            field_dict["current_schema_version"] = current_schema_version
        if source_schema_version is not UNSET:
            field_dict["source_schema_version"] = source_schema_version

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        current_version = d.pop("current_version")

        source_version = d.pop("source_version")

        expected_version_matches = d.pop("expected_version_matches")

        schema_relation = check_durable_entity_restore_preview_schema_relation(d.pop("schema_relation"))

        compatibility = check_durable_entity_restore_preview_compatibility(d.pop("compatibility"))

        alarm_pending = d.pop("alarm_pending")

        outbox_pending = d.pop("outbox_pending")

        alarm_exhausted = d.pop("alarm_exhausted")

        outbox_exhausted = d.pop("outbox_exhausted")

        current_schema_version = d.pop("current_schema_version", UNSET)

        source_schema_version = d.pop("source_schema_version", UNSET)

        durable_entity_restore_preview = cls(
            current_version=current_version,
            source_version=source_version,
            expected_version_matches=expected_version_matches,
            schema_relation=schema_relation,
            compatibility=compatibility,
            alarm_pending=alarm_pending,
            outbox_pending=outbox_pending,
            alarm_exhausted=alarm_exhausted,
            outbox_exhausted=outbox_exhausted,
            current_schema_version=current_schema_version,
            source_schema_version=source_schema_version,
        )

        return durable_entity_restore_preview
