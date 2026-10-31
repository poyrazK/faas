from typing import Literal

TriggerBrokerPoisonStrategy = Literal["commit", "seek-to-offset"]

TRIGGER_BROKER_POISON_STRATEGY_VALUES: set[TriggerBrokerPoisonStrategy] = {
    "commit",
    "seek-to-offset",
}


def check_trigger_broker_poison_strategy(value: str) -> TriggerBrokerPoisonStrategy:
    if value in TRIGGER_BROKER_POISON_STRATEGY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TRIGGER_BROKER_POISON_STRATEGY_VALUES!r}")
