from typing import Literal

EdgeProtectionAICrawlerPurpose = Literal["assistant", "search", "training"]

EDGE_PROTECTION_AI_CRAWLER_PURPOSE_VALUES: set[EdgeProtectionAICrawlerPurpose] = {
    "assistant",
    "search",
    "training",
}


def check_edge_protection_ai_crawler_purpose(value: str) -> EdgeProtectionAICrawlerPurpose:
    if value in EDGE_PROTECTION_AI_CRAWLER_PURPOSE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {EDGE_PROTECTION_AI_CRAWLER_PURPOSE_VALUES!r}")
