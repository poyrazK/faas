from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.log_excerpt_level import LogExcerptLevel, check_log_excerpt_level
from ..models.log_excerpt_source import LogExcerptSource, check_log_excerpt_source
from ..types import UNSET, Unset

T = TypeVar("T", bound="LogExcerpt")


@_attrs_define
class LogExcerpt:
    """One log entry attached to a `Problem` (error-explanations
    cluster, spec §6.4 amendment 1) or persisted on
    `deployments.error_relevant_logs`. The shape mirrors the
    cluster's per-line log wire: `ts` is RFC3339 (apids
    stamp format), `level` is one of `info|warn|error`,
    `source` is the cluster's source discriminator
    (build|vm-init|app|gateway), `message` is ≤512 bytes.

    """

    ts: str | Unset = UNSET
    level: LogExcerptLevel | Unset = UNSET
    source: LogExcerptSource | Unset = UNSET
    message: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        ts = self.ts

        level: str | Unset = UNSET
        if not isinstance(self.level, Unset):
            level = self.level

        source: str | Unset = UNSET
        if not isinstance(self.source, Unset):
            source = self.source

        message = self.message

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if ts is not UNSET:
            field_dict["ts"] = ts
        if level is not UNSET:
            field_dict["level"] = level
        if source is not UNSET:
            field_dict["source"] = source
        if message is not UNSET:
            field_dict["message"] = message

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        ts = d.pop("ts", UNSET)

        _level = d.pop("level", UNSET)
        level: LogExcerptLevel | Unset
        if isinstance(_level, Unset):
            level = UNSET
        else:
            level = check_log_excerpt_level(_level)

        _source = d.pop("source", UNSET)
        source: LogExcerptSource | Unset
        if isinstance(_source, Unset):
            source = UNSET
        else:
            source = check_log_excerpt_source(_source)

        message = d.pop("message", UNSET)

        log_excerpt = cls(
            ts=ts,
            level=level,
            source=source,
            message=message,
        )

        log_excerpt.additional_properties = d
        return log_excerpt

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
