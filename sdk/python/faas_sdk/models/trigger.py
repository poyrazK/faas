from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.trigger_broker_poison_strategy import TriggerBrokerPoisonStrategy, check_trigger_broker_poison_strategy
from ..models.trigger_kind import TriggerKind, check_trigger_kind
from ..models.trigger_source_type_1 import TriggerSourceType1, check_trigger_source_type_1
from ..models.trigger_source_type_2_type_1 import TriggerSourceType2Type1, check_trigger_source_type_2_type_1
from ..models.trigger_source_type_3_type_1 import TriggerSourceType3Type1, check_trigger_source_type_3_type_1
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.filter_criteria import FilterCriteria
    from ..models.trigger_config import TriggerConfig


T = TypeVar("T", bound="Trigger")


@_attrs_define
class Trigger:
    """Read shape returned by GET / POST / PATCH on /v1/triggers.
    The `config` blob is opaque at the wire level — each kind
    decodes its own per-shape struct lazily. The SDK round-trip
    preserves unknown fields across client versions. Kafka
    credentials are never returned: `password_set` and
    `client_key_set` report their presence without exposing
    plaintext or the internal sealed envelope.

    """

    id: str
    account_id: str
    app_id: str
    kind: TriggerKind
    """Discriminator for the underlying event source."""
    enabled: bool
    config: TriggerConfig
    """Per-kind opaque configuration. Decode with the per-kind
    struct (KafkaTriggerConfig, NATSTriggerConfig, etc).
    Kafka responses replace write-only password/client-key
    leaves with boolean `password_set`/`client_key_set` markers.
    """
    batch_size_max: int
    """Records per batch upper bound. Omitted create values use min(64, the account plan cap)."""
    batch_window_ms: int
    """Milliseconds a partial batch may wait before dispatch. Omitted create values use min(1000, the account plan
    cap)."""
    max_attempts: int
    """Omitted create values use min(5, the account plan cap)."""
    payload_max_bytes: int
    """Per-record broker payload byte cap (migration 00274).
    Records above this size are DLQ'd at insert time with
    reason='payload_too_large' rather than silently truncated.
    Plan-level ceiling in /v1/limits TriggerPayloadMaxBytes.
    Omitted create values use min(6291456, the account plan cap).
    """
    broker_poison_strategy: TriggerBrokerPoisonStrategy
    """Kafka-only poison-record handling strategy (migration 00275,
    audit #10). "commit" (default) advances the broker offset
    via CommitMessages when the dispatcher dead-letters a
    record — the broker offset and the DB dead-letter state
    are permanently out of sync for that offset; operator
    retry works via the dashboard's "re-drive from DLQ"
    action which mints a fresh trigger_records row from the
    same item_id. "seek-to-offset" calls SetOffset(msg.Offset)
    instead so the next Poll re-fetches the same message —
    operator retry combines a trigger re-enable with a
    dashboard "reset offset" action that re-fetches the
    dead-lettered payload. No effect on non-kafka kinds.
    """
    created_at: datetime.datetime
    updated_at: datetime.datetime
    slug: str | Unset = UNSET
    """Unique-per-app handle. Required for non-cron kinds; ignored on cron."""
    filter_criteria: FilterCriteria | Unset = UNSET
    """FilterCriteria on a trigger (migration 00300,
    pkg/sched/filter.go). nil / omitted matches every record.
    Top-level arrays combine via implicit OR for `$or` and
    AND for `$and`; nested clauses honour the same shape.
    Jsonpath implementation: github.com/PaesslerAG/jsonpath —
    no eval semantics, no customer-supplied code execution.
    """
    schedule: None | str | Unset = UNSET
    path: None | str | Unset = UNSET
    cron_id: None | str | Unset = UNSET
    source: None | TriggerSourceType1 | TriggerSourceType2Type1 | TriggerSourceType3Type1 | Unset = UNSET
    """Source discriminator for kind=queue rows."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        account_id = self.account_id

        app_id = self.app_id

        kind: str = self.kind

        enabled = self.enabled

        config = self.config.to_dict()

        batch_size_max = self.batch_size_max

        batch_window_ms = self.batch_window_ms

        max_attempts = self.max_attempts

        payload_max_bytes = self.payload_max_bytes

        broker_poison_strategy: str = self.broker_poison_strategy

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        slug = self.slug

        filter_criteria: dict[str, Any] | Unset = UNSET
        if not isinstance(self.filter_criteria, Unset):
            filter_criteria = self.filter_criteria.to_dict()

        schedule: None | str | Unset
        if isinstance(self.schedule, Unset):
            schedule = UNSET
        else:
            schedule = self.schedule

        path: None | str | Unset
        if isinstance(self.path, Unset):
            path = UNSET
        else:
            path = self.path

        cron_id: None | str | Unset
        if isinstance(self.cron_id, Unset):
            cron_id = UNSET
        else:
            cron_id = self.cron_id

        source: None | str | Unset
        if isinstance(self.source, Unset):
            source = UNSET
        elif isinstance(self.source, str):
            source = self.source
        elif isinstance(self.source, str):
            source = self.source
        elif isinstance(self.source, str):
            source = self.source
        else:
            source = self.source

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "account_id": account_id,
                "app_id": app_id,
                "kind": kind,
                "enabled": enabled,
                "config": config,
                "batch_size_max": batch_size_max,
                "batch_window_ms": batch_window_ms,
                "max_attempts": max_attempts,
                "payload_max_bytes": payload_max_bytes,
                "broker_poison_strategy": broker_poison_strategy,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if slug is not UNSET:
            field_dict["slug"] = slug
        if filter_criteria is not UNSET:
            field_dict["filter_criteria"] = filter_criteria
        if schedule is not UNSET:
            field_dict["schedule"] = schedule
        if path is not UNSET:
            field_dict["path"] = path
        if cron_id is not UNSET:
            field_dict["cron_id"] = cron_id
        if source is not UNSET:
            field_dict["source"] = source

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.filter_criteria import FilterCriteria
        from ..models.trigger_config import TriggerConfig

        d = dict(src_dict)
        id = d.pop("id")

        account_id = d.pop("account_id")

        app_id = d.pop("app_id")

        kind = check_trigger_kind(d.pop("kind"))

        enabled = d.pop("enabled")

        config = TriggerConfig.from_dict(d.pop("config"))

        batch_size_max = d.pop("batch_size_max")

        batch_window_ms = d.pop("batch_window_ms")

        max_attempts = d.pop("max_attempts")

        payload_max_bytes = d.pop("payload_max_bytes")

        broker_poison_strategy = check_trigger_broker_poison_strategy(d.pop("broker_poison_strategy"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        slug = d.pop("slug", UNSET)

        _filter_criteria = d.pop("filter_criteria", UNSET)
        filter_criteria: FilterCriteria | Unset
        if isinstance(_filter_criteria, Unset):
            filter_criteria = UNSET
        else:
            filter_criteria = FilterCriteria.from_dict(_filter_criteria)

        def _parse_schedule(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        schedule = _parse_schedule(d.pop("schedule", UNSET))

        def _parse_path(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        path = _parse_path(d.pop("path", UNSET))

        def _parse_cron_id(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        cron_id = _parse_cron_id(d.pop("cron_id", UNSET))

        def _parse_source(
            data: object,
        ) -> None | TriggerSourceType1 | TriggerSourceType2Type1 | TriggerSourceType3Type1 | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                source_type_1 = check_trigger_source_type_1(data)

                return source_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                source_type_2_type_1 = check_trigger_source_type_2_type_1(data)

                return source_type_2_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                source_type_3_type_1 = check_trigger_source_type_3_type_1(data)

                return source_type_3_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | TriggerSourceType1 | TriggerSourceType2Type1 | TriggerSourceType3Type1 | Unset, data)

        source = _parse_source(d.pop("source", UNSET))

        trigger = cls(
            id=id,
            account_id=account_id,
            app_id=app_id,
            kind=kind,
            enabled=enabled,
            config=config,
            batch_size_max=batch_size_max,
            batch_window_ms=batch_window_ms,
            max_attempts=max_attempts,
            payload_max_bytes=payload_max_bytes,
            broker_poison_strategy=broker_poison_strategy,
            created_at=created_at,
            updated_at=updated_at,
            slug=slug,
            filter_criteria=filter_criteria,
            schedule=schedule,
            path=path,
            cron_id=cron_id,
            source=source,
        )

        trigger.additional_properties = d
        return trigger

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
