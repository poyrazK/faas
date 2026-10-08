from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.event_schema_rollout_response_schema_origin import (
    EventSchemaRolloutResponseSchemaOrigin,
    check_event_schema_rollout_response_schema_origin,
)

if TYPE_CHECKING:
    from ..models.event_schema_rollout_consumer import EventSchemaRolloutConsumer
    from ..models.event_schema_rollout_retained import EventSchemaRolloutRetained
    from ..models.event_schema_rollout_validation import EventSchemaRolloutValidation


T = TypeVar("T", bound="EventSchemaRolloutResponse")


@_attrs_define
class EventSchemaRolloutResponse:
    source: str
    type_: str
    version: str
    schema_origin: EventSchemaRolloutResponseSchemaOrigin
    schema_digest: str
    """SHA-256 of the schema bytes checked."""
    observed_at: datetime.datetime
    consumer_count: int
    """Observed matching enabled application subscriptions; a lower bound when truncated."""
    accepting_count: int
    excluding_count: int
    consumers_truncated: bool
    consumers: list[EventSchemaRolloutConsumer]
    sample_valid_count: int
    sample_invalid_count: int
    samples: list[EventSchemaRolloutValidation]
    retained: EventSchemaRolloutRetained

    def to_dict(self) -> dict[str, Any]:
        source = self.source

        type_ = self.type_

        version = self.version

        schema_origin: str = self.schema_origin

        schema_digest = self.schema_digest

        observed_at = self.observed_at.isoformat()

        consumer_count = self.consumer_count

        accepting_count = self.accepting_count

        excluding_count = self.excluding_count

        consumers_truncated = self.consumers_truncated

        consumers = []
        for consumers_item_data in self.consumers:
            consumers_item = consumers_item_data.to_dict()
            consumers.append(consumers_item)

        sample_valid_count = self.sample_valid_count

        sample_invalid_count = self.sample_invalid_count

        samples = []
        for samples_item_data in self.samples:
            samples_item = samples_item_data.to_dict()
            samples.append(samples_item)

        retained = self.retained.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "source": source,
                "type": type_,
                "version": version,
                "schema_origin": schema_origin,
                "schema_digest": schema_digest,
                "observed_at": observed_at,
                "consumer_count": consumer_count,
                "accepting_count": accepting_count,
                "excluding_count": excluding_count,
                "consumers_truncated": consumers_truncated,
                "consumers": consumers,
                "sample_valid_count": sample_valid_count,
                "sample_invalid_count": sample_invalid_count,
                "samples": samples,
                "retained": retained,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_schema_rollout_consumer import EventSchemaRolloutConsumer
        from ..models.event_schema_rollout_retained import EventSchemaRolloutRetained
        from ..models.event_schema_rollout_validation import EventSchemaRolloutValidation

        d = dict(src_dict)
        source = d.pop("source")

        type_ = d.pop("type")

        version = d.pop("version")

        schema_origin = check_event_schema_rollout_response_schema_origin(d.pop("schema_origin"))

        schema_digest = d.pop("schema_digest")

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        consumer_count = d.pop("consumer_count")

        accepting_count = d.pop("accepting_count")

        excluding_count = d.pop("excluding_count")

        consumers_truncated = d.pop("consumers_truncated")

        consumers = []
        _consumers = d.pop("consumers")
        for consumers_item_data in _consumers:
            consumers_item = EventSchemaRolloutConsumer.from_dict(consumers_item_data)

            consumers.append(consumers_item)

        sample_valid_count = d.pop("sample_valid_count")

        sample_invalid_count = d.pop("sample_invalid_count")

        samples = []
        _samples = d.pop("samples")
        for samples_item_data in _samples:
            samples_item = EventSchemaRolloutValidation.from_dict(samples_item_data)

            samples.append(samples_item)

        retained = EventSchemaRolloutRetained.from_dict(d.pop("retained"))

        event_schema_rollout_response = cls(
            source=source,
            type_=type_,
            version=version,
            schema_origin=schema_origin,
            schema_digest=schema_digest,
            observed_at=observed_at,
            consumer_count=consumer_count,
            accepting_count=accepting_count,
            excluding_count=excluding_count,
            consumers_truncated=consumers_truncated,
            consumers=consumers,
            sample_valid_count=sample_valid_count,
            sample_invalid_count=sample_invalid_count,
            samples=samples,
            retained=retained,
        )

        return event_schema_rollout_response
