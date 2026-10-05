from typing import Literal

FinancialPriceCurrency = Literal["EUR"]

FINANCIAL_PRICE_CURRENCY_VALUES: set[FinancialPriceCurrency] = {
    "EUR",
}


def check_financial_price_currency(value: str) -> FinancialPriceCurrency:
    if value in FINANCIAL_PRICE_CURRENCY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_PRICE_CURRENCY_VALUES!r}")
