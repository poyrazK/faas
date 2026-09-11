from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.create_trigger_request_broker_poison_strategy_type_1 import (
    CreateTriggerRequestBrokerPoisonStrategyType1,
    check_create_trigger_request_broker_poison_strategy_type_1,
)
from ..models.create_trigger_request_broker_poison_strategy_type_2_type_1 import (
    CreateTriggerRequestBrokerPoisonStrategyType2Type1,
    check_create_trigger_request_broker_poison_strategy_type_2_type_1,
)
from ..models.create_trigger_request_broker_poison_strategy_type_3_type_1 import (
    CreateTriggerRequestBrokerPoisonStrategyType3Type1,
    check_create_trigger_request_broker_poison_strategy_type_3_type_1,
)
from ..models.trigger_kind import TriggerKind, check_trigger_kind
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.create_trigger_request_config import CreateTriggerRequestConfig
    from ..models.filter_criteria import FilterCriteria


T = TypeVar("T", bound="CreateTriggerRequest")


@_attrs_define
class CreateTriggerRequest:
    """Trigger create payload. Kind is immutable after create. Per-kind
    gating mirrors pkg/gregalemanifest.validateKindConfig:
      - cron: requires schedule + path (slug ignored)
      - non-cron: requires slug + config
    Omitted delivery settings use the platform default capped to the
    selected account plan; explicit over-cap values are rejected.

    """

    app_id: str
    kind: TriggerKind
    """Discriminator for the underlying event source."""
    slug: str | Unset = UNSET
    enabled: bool | None | Unset = UNSET
    config: CreateTriggerRequestConfig | Unset = UNSET
    """Per-kind opaque config blob. Kafka password and client_key leaves are plaintext-on-write and sealed before
    persistence."""
    batch_size_max: int | None | Unset = UNSET
    batch_window_ms: int | None | Unset = UNSET
    max_attempts: int | None | Unset = UNSET
    payload_max_bytes: int | None | Unset = UNSET
    broker_poison_strategy: (
        CreateTriggerRequestBrokerPoisonStrategyType1
        | CreateTriggerRequestBrokerPoisonStrategyType2Type1
        | CreateTriggerRequestBrokerPoisonStrategyType3Type1
        | None
        | Unset
    ) = UNSET
    """Kafka-only poison-record handling strategy. null/omitted
    falls through to the DB default 'commit'. Same semantics
    as the Trigger read shape.
    """
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
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        kind: str = self.kind

        slug = self.slug

        enabled: bool | None | Unset
        if isinstance(self.enabled, Unset):
            enabled = UNSET
        else:
            enabled = self.enabled

        config: dict[str, Any] | Unset = UNSET
        if not isinstance(self.config, Unset):
            config = self.config.to_dict()

        batch_size_max: int | None | Unset
        if isinstance(self.batch_size_max, Unset):
            batch_size_max = UNSET
        else:
            batch_size_max = self.batch_size_max

        batch_window_ms: int | None | Unset
        if isinstance(self.batch_window_ms, Unset):
            batch_window_ms = UNSET
        else:
            batch_window_ms = self.batch_window_ms

        max_attempts: int | None | Unset
        if isinstance(self.max_attempts, Unset):
            max_attempts = UNSET
        else:
            max_attempts = self.max_attempts

        payload_max_bytes: int | None | Unset
        if isinstance(self.payload_max_bytes, Unset):
            payload_max_bytes = UNSET
        else:
            payload_max_bytes = self.payload_max_bytes

        broker_poison_strategy: None | str | Unset
        if isinstance(self.broker_poison_strategy, Unset):
            broker_poison_strategy = UNSET
        elif isinstance(self.broker_poison_strategy, str):
            broker_poison_strategy = self.broker_poison_strategy
        elif isinstance(self.broker_poison_strategy, str):
            broker_poison_strategy = self.broker_poison_strategy
        elif isinstance(self.broker_poison_strategy, str):
            broker_poison_strategy = self.broker_poison_strategy
        else:
            broker_poison_strategy = self.broker_poison_strategy

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

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "kind": kind,
            }
        )
        if slug is not UNSET:
            field_dict["slug"] = slug
        if enabled is not UNSET:
            field_dict["enabled"] = enabled
        if config is not UNSET:
            field_dict["config"] = config
        if batch_size_max is not UNSET:
            field_dict["batch_size_max"] = batch_size_max
        if batch_window_ms is not UNSET:
            field_dict["batch_window_ms"] = batch_window_ms
        if max_attempts is not UNSET:
            field_dict["max_attempts"] = max_attempts
        if payload_max_bytes is not UNSET:
            field_dict["payload_max_bytes"] = payload_max_bytes
        if broker_poison_strategy is not UNSET:
            field_dict["broker_poison_strategy"] = broker_poison_strategy
        if filter_criteria is not UNSET:
            field_dict["filter_criteria"] = filter_criteria
        if schedule is not UNSET:
            field_dict["schedule"] = schedule
        if path is not UNSET:
            field_dict["path"] = path

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.create_trigger_request_config import CreateTriggerRequestConfig
        from ..models.filter_criteria import FilterCriteria

        d = dict(src_dict)
        app_id = d.pop("app_id")

        kind = check_trigger_kind(d.pop("kind"))

        slug = d.pop("slug", UNSET)

        def _parse_enabled(data: object) -> bool | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(bool | None | Unset, data)

        enabled = _parse_enabled(d.pop("enabled", UNSET))

        _config = d.pop("config", UNSET)
        config: CreateTriggerRequestConfig | Unset
        if isinstance(_config, Unset):
            config = UNSET
        else:
            config = CreateTriggerRequestConfig.from_dict(_config)

        def _parse_batch_size_max(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        batch_size_max = _parse_batch_size_max(d.pop("batch_size_max", UNSET))

        def _parse_batch_window_ms(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        batch_window_ms = _parse_batch_window_ms(d.pop("batch_window_ms", UNSET))

        def _parse_max_attempts(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        max_attempts = _parse_max_attempts(d.pop("max_attempts", UNSET))

        def _parse_payload_max_bytes(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        payload_max_bytes = _parse_payload_max_bytes(d.pop("payload_max_bytes", UNSET))

        def _parse_broker_poison_strategy(
            data: object,
        ) -> (
            CreateTriggerRequestBrokerPoisonStrategyType1
            | CreateTriggerRequestBrokerPoisonStrategyType2Type1
            | CreateTriggerRequestBrokerPoisonStrategyType3Type1
            | None
            | Unset
        ):
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                broker_poison_strategy_type_1 = check_create_trigger_request_broker_poison_strategy_type_1(data)

                return broker_poison_strategy_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                broker_poison_strategy_type_2_type_1 = (
                    check_create_trigger_request_broker_poison_strategy_type_2_type_1(data)
                )

                return broker_poison_strategy_type_2_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, str):
                    raise TypeError()
                broker_poison_strategy_type_3_type_1 = (
                    check_create_trigger_request_broker_poison_strategy_type_3_type_1(data)
                )

                return broker_poison_strategy_type_3_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(
                CreateTriggerRequestBrokerPoisonStrategyType1
                | CreateTriggerRequestBrokerPoisonStrategyType2Type1
                | CreateTriggerRequestBrokerPoisonStrategyType3Type1
                | None
                | Unset,
                data,
            )

        broker_poison_strategy = _parse_broker_poison_strategy(d.pop("broker_poison_strategy", UNSET))

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

        create_trigger_request = cls(
            app_id=app_id,
            kind=kind,
            slug=slug,
            enabled=enabled,
            config=config,
            batch_size_max=batch_size_max,
            batch_window_ms=batch_window_ms,
            max_attempts=max_attempts,
            payload_max_bytes=payload_max_bytes,
            broker_poison_strategy=broker_poison_strategy,
            filter_criteria=filter_criteria,
            schedule=schedule,
            path=path,
        )

        create_trigger_request.additional_properties = d
        return create_trigger_request

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
