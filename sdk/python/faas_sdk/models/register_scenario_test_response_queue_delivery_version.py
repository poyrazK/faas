from typing import Literal

RegisterScenarioTestResponseQueueDeliveryVersion = Literal[1]

REGISTER_SCENARIO_TEST_RESPONSE_QUEUE_DELIVERY_VERSION_VALUES: set[RegisterScenarioTestResponseQueueDeliveryVersion] = {
    1,
}


def check_register_scenario_test_response_queue_delivery_version(
    value: int,
) -> RegisterScenarioTestResponseQueueDeliveryVersion:
    if value in REGISTER_SCENARIO_TEST_RESPONSE_QUEUE_DELIVERY_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REGISTER_SCENARIO_TEST_RESPONSE_QUEUE_DELIVERY_VERSION_VALUES!r}"
    )
