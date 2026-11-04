from typing import Literal

KafkaSASLMechanism = Literal["PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512"]

KAFKA_SASL_MECHANISM_VALUES: set[KafkaSASLMechanism] = {
    "PLAIN",
    "SCRAM-SHA-256",
    "SCRAM-SHA-512",
}


def check_kafka_sasl_mechanism(value: str) -> KafkaSASLMechanism:
    if value in KAFKA_SASL_MECHANISM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {KAFKA_SASL_MECHANISM_VALUES!r}")
