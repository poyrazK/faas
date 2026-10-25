from typing import Literal

CreateTriggerRequestBrokerPoisonStrategyType2Type1 = Literal["commit", "seek-to-offset"]

CREATE_TRIGGER_REQUEST_BROKER_POISON_STRATEGY_TYPE_2_TYPE_1_VALUES: set[
    CreateTriggerRequestBrokerPoisonStrategyType2Type1
] = {
    "commit",
    "seek-to-offset",
}


def check_create_trigger_request_broker_poison_strategy_type_2_type_1(
    value: str,
) -> CreateTriggerRequestBrokerPoisonStrategyType2Type1:
    if value in CREATE_TRIGGER_REQUEST_BROKER_POISON_STRATEGY_TYPE_2_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_TRIGGER_REQUEST_BROKER_POISON_STRATEGY_TYPE_2_TYPE_1_VALUES!r}"
    )
