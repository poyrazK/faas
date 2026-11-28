"""Pure Prometheus text 0.0.4; no registry, listener or retained snapshots."""

from __future__ import annotations

from ._event_acknowledgment_preparation_prometheus_validation import (
    _DEFAULT_PREFIX,
    _INVALID_EXPOSITION,
    _validated,
)
from .event_acknowledgment_preparation_metrics import (
    EventAcknowledgmentPreparationHistogram,
    EventAcknowledgmentPreparationMetricsSnapshot,
)

EVENT_ACKNOWLEDGMENT_PREPARATION_PROMETHEUS_CONTENT_TYPE = "text/plain; version=0.0.4; charset=utf-8"


def _seconds(milliseconds: int) -> str:
    # Preserve the whole-millisecond source exactly in decimal text, without float rounding.
    whole, fraction = divmod(milliseconds, 1000)
    return str(whole) if fraction == 0 else f"{whole}.{fraction:03d}".rstrip("0")


def format_event_acknowledgment_preparation_metrics(
    snapshot: EventAcknowledgmentPreparationMetricsSnapshot, *, prefix: str = _DEFAULT_PREFIX
) -> str:
    """Render a complete cumulative snapshot with fixed labels, seconds and a final newline.

    Reject malformed current-contract snapshots/prefixes with a generic ValueError
    before producing text. Export outside the processing observer.
    """
    validated = None
    try:
        validated = _validated(snapshot, prefix)
    except BaseException:
        pass
    del snapshot, prefix
    if validated is None:
        # Raise outside validation's exception context, without rejected input in this frame.
        raise ValueError(_INVALID_EXPOSITION)
    metric_prefix, data = validated
    lines: list[str] = []

    def family(suffix: str, help_text: str, metric_type: str) -> str:
        name = f"{metric_prefix}_{suffix}"
        lines.extend((f"# HELP {name} {help_text}", f"# TYPE {name} {metric_type}"))
        return name

    def counter(suffix: str, help_text: str, label: str, values: dict[str, int]) -> None:
        name = family(suffix, help_text, "counter")
        for key, value in values.items():
            lines.append(f'{name}{{{label}="{key}"}} {value}')

    counter("events_total", "Accepted preparation lifecycle observations by event kind.", "kind", data["event_counts"])
    counter(
        "operations_total",
        "Prepared operation shutdown observations by outcome.",
        "outcome",
        data["operation_outcomes"],
    )
    counter(
        "callbacks_total",
        "Preparation callback settlement observations by outcome.",
        "outcome",
        data["preparation_outcomes"],
    )
    counter(
        "local_commits_total",
        "Confirmed local receipt and acknowledgment intent commits by receipt disposition.",
        "receipt",
        {"new": data["local_commits"]["new_receipt"], "replayed": data["local_commits"]["replayed_receipt"]},
    )
    renewals = family("renewal_failures_total", "Observed lease renewal failures by lease and reason.", "counter")
    for lease in ("local", "platform"):
        for reason, value in data["renewal_failures"][lease].items():
            lines.append(f'{renewals}{{lease="{lease}",reason="{reason}"}} {value}')
    counter(
        "unknown_commits_total",
        "Uncertain local commit observations by stage.",
        "stage",
        data["unknown_commits_by_stage"],
    )

    def duration(suffix: str, help_text: str, value: EventAcknowledgmentPreparationHistogram) -> None:
        name = family(suffix, help_text, "histogram")
        for bucket in value["buckets"]:
            bound = "+Inf" if bucket["upper_bound_ms"] is None else _seconds(bucket["upper_bound_ms"])
            lines.append(f'{name}_bucket{{le="{bound}"}} {bucket["count"]}')
        lines.extend((f"{name}_sum {_seconds(value['sum_ms'])}", f"{name}_count {value['count']}"))

    duration(
        "callback_duration_seconds",
        "Preparation callback settlement duration in seconds.",
        data["preparation_duration"],
    )
    duration(
        "operation_duration_seconds",
        "Prepared operation duration through shutdown cleanup in seconds.",
        data["operation_duration"],
    )
    saturated = family(
        "saturated", "Sticky safe integer clipping flag by scope; clipped values are lower bounds.", "gauge"
    )
    lines.extend(
        (
            f'{saturated}{{scope="collector"}} {int(data["saturated"])}',
            f'{saturated}{{scope="callback_duration"}} {int(data["preparation_duration"]["saturated"])}',
            f'{saturated}{{scope="operation_duration"}} {int(data["operation_duration"]["saturated"])}',
        )
    )
    return "\n".join(lines) + "\n"
