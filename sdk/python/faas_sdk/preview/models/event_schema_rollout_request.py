from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="EventSchemaRolloutRequest")


@_attrs_define
class EventSchemaRolloutRequest:
    """Candidate event schema and bounded retained-event sample settings for a read-only rollout preview."""

    source: str
    """Concrete event source; wildcard patterns are not allowed."""
    type_: str
    """Concrete event type; wildcard patterns are not allowed."""
    version: str
    schema: Any | Unset = UNSET
    """Proposed Draft 2020-12 JSON Schema, at most 64 KiB; external references are forbidden. Omit to use the
    registered version. It is never registered by this request."""
    samples: list[Any] | Unset = UNSET
    """Event data values to validate, at most 64 KiB each. No event envelopes are required."""
    from_: datetime.datetime | Unset = UNSET
    """Optional inclusive platform acceptance time; requires until."""
    until: datetime.datetime | Unset = UNSET
    """Optional exclusive acceptance time; requires from. Cut off at observed_at when in the future."""
    retained_limit: int | Unset = 0
    """Requires a retained range when nonzero. Zero or omitted checks up to 100 matching payloads when a range is
    supplied."""

    def to_dict(self) -> dict[str, Any]:
        source = self.source

        type_ = self.type_

        version = self.version

        schema = self.schema

        samples: list[Any] | Unset = UNSET
        if not isinstance(self.samples, Unset):
            samples = self.samples

        from_: str | Unset = UNSET
        if not isinstance(self.from_, Unset):
            from_ = self.from_.isoformat()

        until: str | Unset = UNSET
        if not isinstance(self.until, Unset):
            until = self.until.isoformat()

        retained_limit = self.retained_limit

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source": source,
                "type": type_,
                "version": version,
            }
        )
        if schema is not UNSET:
            field_dict["schema"] = schema
        if samples is not UNSET:
            field_dict["samples"] = samples
        if from_ is not UNSET:
            field_dict["from"] = from_
        if until is not UNSET:
            field_dict["until"] = until
        if retained_limit is not UNSET:
            field_dict["retained_limit"] = retained_limit

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        source = d.pop("source")

        type_ = d.pop("type")

        version = d.pop("version")

        schema = d.pop("schema", UNSET)

        samples = cast(list[Any], d.pop("samples", UNSET))

        _from_ = d.pop("from", UNSET)
        from_: datetime.datetime | Unset
        if isinstance(_from_, Unset):
            from_ = UNSET
        else:
            from_ = datetime.datetime.fromisoformat(_from_)

        _until = d.pop("until", UNSET)
        until: datetime.datetime | Unset
        if isinstance(_until, Unset):
            until = UNSET
        else:
            until = datetime.datetime.fromisoformat(_until)

        retained_limit = d.pop("retained_limit", UNSET)

        event_schema_rollout_request = cls(
            source=source,
            type_=type_,
            version=version,
            schema=schema,
            samples=samples,
            from_=from_,
            until=until,
            retained_limit=retained_limit,
        )

        return event_schema_rollout_request
