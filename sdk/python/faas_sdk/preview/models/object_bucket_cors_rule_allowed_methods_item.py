from typing import Literal

ObjectBucketCORSRuleAllowedMethodsItem = Literal["DELETE", "GET", "HEAD", "POST", "PUT"]

OBJECT_BUCKET_CORS_RULE_ALLOWED_METHODS_ITEM_VALUES: set[ObjectBucketCORSRuleAllowedMethodsItem] = {
    "DELETE",
    "GET",
    "HEAD",
    "POST",
    "PUT",
}


def check_object_bucket_cors_rule_allowed_methods_item(value: str) -> ObjectBucketCORSRuleAllowedMethodsItem:
    if value in OBJECT_BUCKET_CORS_RULE_ALLOWED_METHODS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBJECT_BUCKET_CORS_RULE_ALLOWED_METHODS_ITEM_VALUES!r}"
    )
