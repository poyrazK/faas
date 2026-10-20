from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OutboundRequestPolicy")


@_attrs_define
class OutboundRequestPolicy:
    """Effective customer-selected per-integration policy, bounded by the account plan."""

    rate_per_second: float
    burst: int
    max_in_flight: int
    request_timeout_ms: int
    max_retries: int | Unset = 0
    """Extra attempts for bodyless GET/HEAD requests after selected transient failures. Retries share the request
    timeout and count as one admission."""
    response_cache_ttl_seconds: int | Unset = 0
    """Opt-in maximum freshness for eligible bodyless GET responses. Zero disables caching; provider cache
    directives can shorten or prohibit storage."""
    circuit_breaker_failure_threshold: int | Unset = 0
    """Consecutive transient provider failures before the integration circuit opens. Zero disables the breaker and
    requires circuit_breaker_open_seconds to be zero too."""
    circuit_breaker_open_seconds: int | Unset = 0
    """Cool-down after the breaker opens; after it elapses, only one cross-replica half-open provider probe is
    allowed. Must be set with a nonzero failure threshold."""
    retry_budget_per_minute: int | Unset = 0
    """Shared token-bucket cap on extra safe-method provider attempts per minute across gateway replicas. Capacity
    equals the configured rate; zero disables this aggregate cap while max_retries remains the per-call ceiling."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rate_per_second = self.rate_per_second

        burst = self.burst

        max_in_flight = self.max_in_flight

        request_timeout_ms = self.request_timeout_ms

        max_retries = self.max_retries

        response_cache_ttl_seconds = self.response_cache_ttl_seconds

        circuit_breaker_failure_threshold = self.circuit_breaker_failure_threshold

        circuit_breaker_open_seconds = self.circuit_breaker_open_seconds

        retry_budget_per_minute = self.retry_budget_per_minute

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "rate_per_second": rate_per_second,
                "burst": burst,
                "max_in_flight": max_in_flight,
                "request_timeout_ms": request_timeout_ms,
            }
        )
        if max_retries is not UNSET:
            field_dict["max_retries"] = max_retries
        if response_cache_ttl_seconds is not UNSET:
            field_dict["response_cache_ttl_seconds"] = response_cache_ttl_seconds
        if circuit_breaker_failure_threshold is not UNSET:
            field_dict["circuit_breaker_failure_threshold"] = circuit_breaker_failure_threshold
        if circuit_breaker_open_seconds is not UNSET:
            field_dict["circuit_breaker_open_seconds"] = circuit_breaker_open_seconds
        if retry_budget_per_minute is not UNSET:
            field_dict["retry_budget_per_minute"] = retry_budget_per_minute

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        rate_per_second = d.pop("rate_per_second")

        burst = d.pop("burst")

        max_in_flight = d.pop("max_in_flight")

        request_timeout_ms = d.pop("request_timeout_ms")

        max_retries = d.pop("max_retries", UNSET)

        response_cache_ttl_seconds = d.pop("response_cache_ttl_seconds", UNSET)

        circuit_breaker_failure_threshold = d.pop("circuit_breaker_failure_threshold", UNSET)

        circuit_breaker_open_seconds = d.pop("circuit_breaker_open_seconds", UNSET)

        retry_budget_per_minute = d.pop("retry_budget_per_minute", UNSET)

        outbound_request_policy = cls(
            rate_per_second=rate_per_second,
            burst=burst,
            max_in_flight=max_in_flight,
            request_timeout_ms=request_timeout_ms,
            max_retries=max_retries,
            response_cache_ttl_seconds=response_cache_ttl_seconds,
            circuit_breaker_failure_threshold=circuit_breaker_failure_threshold,
            circuit_breaker_open_seconds=circuit_breaker_open_seconds,
            retry_budget_per_minute=retry_budget_per_minute,
        )

        outbound_request_policy.additional_properties = d
        return outbound_request_policy

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
