"""Create a versioned task publication manifest for the selected environment."""
import json
import sys
from pathlib import Path

root = Path(__file__).resolve().parent
manifest = {"name": "pdf-report", "version": 1, "definition": {
    "runtime": "python313", "environment_id": sys.argv[1],
    "source": (root / "handler.py").read_text(),
    "input_schema": {"type": "object", "required": ["title"], "additionalProperties": False,
                     "properties": {"title": {"type": "string", "minLength": 1, "maxLength": 120}}},
    "result_schema": {"type": "object", "required": ["pages", "words", "preview_truncated"],
                      "additionalProperties": False,
                      "properties": {"pages": {"type": "integer", "minimum": 0},
                                     "words": {"type": "integer", "minimum": 0},
                                     "preview_truncated": {"type": "boolean"}}},
    "output_files": ["report.html"], "artifact_storage": "stored",
    "limits": {"execution_class": "extended", "timeout_ms": 120000,
               "memory_mb": 512, "ephemeral_disk_mb": 128,
               "max_output_bytes": 32768, "max_artifact_bytes": 262144}}}
Path(sys.argv[2]).write_text(json.dumps(manifest, indent=2) + "\n")
