from typing import Literal

ListStatementInvoicesStatus = Literal["deleted", "draft", "open", "overdue", "paid", "uncollectible", "unpaid", "void"]

LIST_STATEMENT_INVOICES_STATUS_VALUES: set[ListStatementInvoicesStatus] = {
    "deleted",
    "draft",
    "open",
    "overdue",
    "paid",
    "uncollectible",
    "unpaid",
    "void",
}


def check_list_statement_invoices_status(value: str) -> ListStatementInvoicesStatus:
    if value in LIST_STATEMENT_INVOICES_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_STATEMENT_INVOICES_STATUS_VALUES!r}")
