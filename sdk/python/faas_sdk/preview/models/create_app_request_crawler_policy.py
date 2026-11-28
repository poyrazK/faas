from typing import Literal

CreateAppRequestCrawlerPolicy = Literal["block", "cached", "wake"]

CREATE_APP_REQUEST_CRAWLER_POLICY_VALUES: set[CreateAppRequestCrawlerPolicy] = {
    "block",
    "cached",
    "wake",
}


def check_create_app_request_crawler_policy(value: str) -> CreateAppRequestCrawlerPolicy:
    if value in CREATE_APP_REQUEST_CRAWLER_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CREATE_APP_REQUEST_CRAWLER_POLICY_VALUES!r}")
