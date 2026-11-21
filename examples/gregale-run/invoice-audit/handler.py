"""Validate structured invoices, with exact decimal parsing and integer cents."""
import csv
import json
import re
from decimal import Decimal
from pathlib import Path


def cents(value):
    if not isinstance(value, str) or not re.fullmatch(r"[0-9]{1,7}\.[0-9]{2}", value):
        raise ValueError("invalid_money")
    return int(Decimal(value) * 100)


def main(input, context):
    invoices = input.get("invoices") if isinstance(input, dict) else None
    if not isinstance(invoices, list) or not 1 <= len(invoices) <= 1000:
        raise ValueError("Expected 1..1000 structured invoices")
    records, seen = [], set()
    accepted_total = 0
    for index, invoice in enumerate(invoices):
        errors = []
        if not isinstance(invoice, dict):
            records.append({"index": index, "invoice_id": "", "accepted": False, "errors": ["invalid_invoice"]})
            continue
        invoice_id = invoice.get("invoice_id", "")
        if not isinstance(invoice_id, str) or not invoice_id or len(invoice_id) > 96:
            errors.append("invalid_invoice_id")
            invoice_id = ""
        elif invoice_id in seen:
            errors.append("duplicate_invoice_id")
        else:
            seen.add(invoice_id)
        items = invoice.get("items")
        total = 0
        if not isinstance(items, list) or not 1 <= len(items) <= 100:
            errors.append("invalid_items")
        else:
            for item in items:
                if not isinstance(item, dict):
                    errors.append("invalid_item")
                    continue
                quantity = item.get("quantity")
                if type(quantity) is not int or not 1 <= quantity <= 10000:
                    errors.append("invalid_quantity")
                    continue
                try:
                    total += quantity * cents(item.get("unit_price"))
                except ValueError as error:
                    errors.append(str(error))
        try:
            claimed = cents(invoice.get("invoice_total"))
            if not errors and total != claimed:
                errors.append("total_mismatch")
        except ValueError as error:
            errors.append(str(error))
        errors = sorted(set(errors))
        record = {"index": index, "invoice_id": invoice_id, "accepted": not errors, "errors": errors}
        if not errors:
            record["total_cents"] = total
            accepted_total += total
        records.append(record)
    if accepted_total > 9007199254740991:
        raise ValueError("Accepted total exceeds the safe JSON integer range")
    accepted = sum(record["accepted"] for record in records)
    result = {"currency": "USD", "checked": len(records), "accepted": accepted,
              "rejected": len(records) - accepted, "accepted_total_cents": accepted_total}
    output = Path(context["output_dir"])
    (output / "audit.json").write_text(json.dumps({"summary": result, "invoices": records}, indent=2) + "\n", encoding="utf-8")
    with (output / "rejected.csv").open("w", newline="", encoding="utf-8") as file:
        writer = csv.writer(file)
        writer.writerow(["index", "invoice_id", "errors"])
        for record in records:
            if not record["accepted"]:
                # CSV is data. Prefix spreadsheet formula markers in user IDs.
                name = record["invoice_id"]
                if name.startswith(("=", "+", "-", "@", "\t", "\r", "\n")):
                    name = "'" + name
                writer.writerow([record["index"], name, ";".join(record["errors"])])
    return result
