from typing import Literal

FinancialPriceContractDeliveryMode = Literal["live", "off", "shadow"]

FINANCIAL_PRICE_CONTRACT_DELIVERY_MODE_VALUES: set[FinancialPriceContractDeliveryMode] = {
    "live",
    "off",
    "shadow",
}


def check_financial_price_contract_delivery_mode(value: str) -> FinancialPriceContractDeliveryMode:
    if value in FINANCIAL_PRICE_CONTRACT_DELIVERY_MODE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {FINANCIAL_PRICE_CONTRACT_DELIVERY_MODE_VALUES!r}")
