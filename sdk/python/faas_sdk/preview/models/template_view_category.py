from typing import Literal

TemplateViewCategory = Literal["ai", "function", "hello", "stateless-contract"]

TEMPLATE_VIEW_CATEGORY_VALUES: set[TemplateViewCategory] = {
    "ai",
    "function",
    "hello",
    "stateless-contract",
}


def check_template_view_category(value: str) -> TemplateViewCategory:
    if value in TEMPLATE_VIEW_CATEGORY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {TEMPLATE_VIEW_CATEGORY_VALUES!r}")
