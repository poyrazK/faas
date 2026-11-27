"""A bounded PDF text report using the environment's pinned Poppler tools."""
import html
import subprocess
from pathlib import Path


def main(input, context):
    title = input["title"]
    if not isinstance(title, str) or not 1 <= len(title) <= 120:
        raise ValueError("Expected a title of 1..120 characters")
    source = Path(context["input_dir"]) / "input.pdf"
    output = Path(context["output_dir"])
    text_path = output / "extracted.txt"
    subprocess.run(
        ["pdftotext", "-enc", "UTF-8", str(source), str(text_path)],
        check=True, timeout=30, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    if text_path.stat().st_size > 1 << 20:
        raise ValueError("Extracted PDF text exceeds the report limit")
    text = text_path.read_text(encoding="utf-8")
    text_path.unlink()
    preview = text[:16000]
    document = (
        "<!doctype html><meta charset=utf-8><title>" + html.escape(title) + "</title>"
        "<h1>" + html.escape(title) + "</h1><pre>" + html.escape(preview) + "</pre>"
    )
    (output / "report.html").write_text(document, encoding="utf-8")
    return {"pages": len([page for page in text.split("\f") if page.strip()]),
            "words": len(text.split()), "preview_truncated": len(text) > len(preview)}
