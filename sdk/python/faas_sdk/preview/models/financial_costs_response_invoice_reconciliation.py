from typing import Literal

FinancialCostsResponseInvoiceReconciliation = Literal["not_reconciled"]

FINANCIAL_COSTS_RESPONSE_INVOICE_RECONCILIATION_VALUES: set[FinancialCostsResponseInvoiceReconciliation] = {
    "not_reconciled",
}


def check_financial_costs_response_invoice_reconciliation(value: str) -> FinancialCostsResponseInvoiceReconciliation:
    if value in FINANCIAL_COSTS_RESPONSE_INVOICE_RECONCILIATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {FINANCIAL_COSTS_RESPONSE_INVOICE_RECONCILIATION_VALUES!r}"
    )
