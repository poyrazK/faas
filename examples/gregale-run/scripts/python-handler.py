"""Local adapter for the documented Python Run handler signature; no VM emulation."""
import importlib.util
import json
import sys
from pathlib import Path

source, input_path, output, result = map(Path, sys.argv[1:])
spec = importlib.util.spec_from_file_location("example_handler", source)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
value = module.main(json.loads(input_path.read_text(encoding="utf-8")), {
    "execution_id": "local-example", "runtime": "python313",
    "input_dir": str(input_path.parent), "output_dir": str(output),
})
result.write_text(json.dumps(value, allow_nan=False, indent=2) + "\n", encoding="utf-8")
