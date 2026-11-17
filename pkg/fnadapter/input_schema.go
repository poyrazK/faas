package fnadapter

// Callee-side input checks for typed function-to-function calls
// (ADR-20261111150000 §1). imaged stamps a function's input schema as
// FAAS_FUNCTION_INPUT_SCHEMA; the adapter checks the body of every request an
// SDK functions client marks with X-Gregale-Function-Call: 1 and answers a
// 422 function_input_invalid problem when it does not match. Other requests
// (browsers, webhooks, plain service calls) are never checked, so declaring a
// schema cannot break a function's existing traffic.
//
// The check covers a JSON Schema subset: type, enum, const, properties,
// required, additionalProperties, items, minItems/maxItems,
// minLength/maxLength, pattern, minimum/maximum and
// exclusiveMinimum/exclusiveMaximum. Keywords outside it are ignored, so a
// richer schema still checks what the subset can and never rejects on the rest.

// NodeInputSchema is spliced into the Node adapter. It defines
// functionInputProblem(env, headers): a problem object for an invalid
// typed-client call, or null.
const NodeInputSchema = `
const functionInputSchema = (() => {
  const raw = process.env.FAAS_FUNCTION_INPUT_SCHEMA;
  if (!raw) return null;
  try {
    const schema = JSON.parse(raw);
    return schema && typeof schema === "object" && !Array.isArray(schema) ? schema : null;
  } catch (_) {
    console.error("gregale: ignoring FAAS_FUNCTION_INPUT_SCHEMA that is not JSON");
    return null;
  }
})();
const schemaJSONType = (value, type) => {
  switch (type) {
    case "null": return value === null;
    case "boolean": return typeof value === "boolean";
    case "number": return typeof value === "number" && Number.isFinite(value);
    case "integer": return Number.isInteger(value);
    case "string": return typeof value === "string";
    case "array": return Array.isArray(value);
    case "object": return value !== null && typeof value === "object" && !Array.isArray(value);
    default: return true;
  }
};
const schemaSame = (a, b) => JSON.stringify(a) === JSON.stringify(b);
function schemaError(schema, value, at) {
  if (!schema || typeof schema !== "object" || Array.isArray(schema)) return null;
  if ("const" in schema && !schemaSame(schema.const, value)) return at + " must be " + JSON.stringify(schema.const);
  if (Array.isArray(schema.enum) && !schema.enum.some((option) => schemaSame(option, value))) {
    return at + " must be one of " + JSON.stringify(schema.enum);
  }
  const types = schema.type === undefined ? [] : [].concat(schema.type);
  if (types.length > 0 && !types.some((type) => schemaJSONType(value, type))) return at + " must be " + types.join(" or ");
  if (typeof value === "string") {
    const length = [...value].length;
    if (typeof schema.minLength === "number" && length < schema.minLength) return at + " must be at least " + schema.minLength + " characters";
    if (typeof schema.maxLength === "number" && length > schema.maxLength) return at + " must be at most " + schema.maxLength + " characters";
    if (typeof schema.pattern === "string") {
      let re = null;
      try { re = new RegExp(schema.pattern, "u"); } catch (_) {}
      if (re && !re.test(value)) return at + " must match " + schema.pattern;
    }
  }
  if (typeof value === "number") {
    if (typeof schema.minimum === "number" && value < schema.minimum) return at + " must be >= " + schema.minimum;
    if (typeof schema.maximum === "number" && value > schema.maximum) return at + " must be <= " + schema.maximum;
    if (typeof schema.exclusiveMinimum === "number" && value <= schema.exclusiveMinimum) return at + " must be > " + schema.exclusiveMinimum;
    if (typeof schema.exclusiveMaximum === "number" && value >= schema.exclusiveMaximum) return at + " must be < " + schema.exclusiveMaximum;
  }
  if (Array.isArray(value)) {
    if (typeof schema.minItems === "number" && value.length < schema.minItems) return at + " must have at least " + schema.minItems + " items";
    if (typeof schema.maxItems === "number" && value.length > schema.maxItems) return at + " must have at most " + schema.maxItems + " items";
    if (schema.items && typeof schema.items === "object" && !Array.isArray(schema.items)) {
      for (let i = 0; i < value.length; i++) {
        const err = schemaError(schema.items, value[i], at + "[" + i + "]");
        if (err) return err;
      }
    }
  }
  if (schemaJSONType(value, "object")) {
    const properties = schema.properties && typeof schema.properties === "object" ? schema.properties : {};
    if (Array.isArray(schema.required)) {
      for (const name of schema.required) {
        if (!Object.prototype.hasOwnProperty.call(value, name)) return at + "." + name + " is required";
      }
    }
    for (const [name, item] of Object.entries(value)) {
      if (Object.prototype.hasOwnProperty.call(properties, name)) {
        const err = schemaError(properties[name], item, at + "." + name);
        if (err) return err;
      } else if (schema.additionalProperties === false) {
        return at + "." + name + " is not allowed";
      } else if (schema.additionalProperties && typeof schema.additionalProperties === "object") {
        const err = schemaError(schema.additionalProperties, item, at + "." + name);
        if (err) return err;
      }
    }
  }
  return null;
}
function functionInputProblem(env, headers) {
  if (!functionInputSchema) return null;
  let marked = false;
  for (const [key, value] of Object.entries(headers || {})) {
    if (key.toLowerCase() === "x-gregale-function-call") marked = [].concat(value).includes("1");
  }
  if (!marked) return null;
  const raw = Buffer.from(env.body_b64 || "", "base64").toString("utf8");
  let input = null;
  if (raw.trim() !== "") {
    try { input = JSON.parse(raw); } catch (_) { return "input must be JSON"; }
  }
  return schemaError(functionInputSchema, input, "input");
}
function emitInputProblem(out, detail) {
  emitWhole(out, 422, { "content-type": "application/problem+json" }, Buffer.from(JSON.stringify({
    type: "https://gregale.dev/docs/errors#function_input_invalid",
    title: "Function input invalid",
    status: 422,
    code: "function_input_invalid",
    detail,
    docs_url: "https://gregale.dev/docs/errors#function_input_invalid",
  })));
}
`

// PythonInputSchema is spliced into the Python adapter. It defines
// _input_problem(request): a 422 envelope for an invalid typed-client call,
// or None.
const PythonInputSchema = `
def _load_input_schema():
    raw = os.environ.get("FAAS_FUNCTION_INPUT_SCHEMA", "")
    if not raw:
        return None
    try:
        schema = json.loads(raw)
    except ValueError:
        print("gregale: ignoring FAAS_FUNCTION_INPUT_SCHEMA that is not JSON", file=sys.stderr, flush=True)
        return None
    return schema if isinstance(schema, dict) else None

_input_schema = _load_input_schema()

def _schema_type(value, kind):
    if kind == "null": return value is None
    if kind == "boolean": return isinstance(value, bool)
    if kind == "number": return isinstance(value, (int, float)) and not isinstance(value, bool)
    if kind == "integer":
        if isinstance(value, bool): return False
        return isinstance(value, int) or (isinstance(value, float) and value.is_integer())
    if kind == "string": return isinstance(value, str)
    if kind == "array": return isinstance(value, list)
    if kind == "object": return isinstance(value, dict)
    return True

def _schema_same(a, b):
    return json.dumps(a, sort_keys=True) == json.dumps(b, sort_keys=True)

def _schema_error(schema, value, at):
    import re
    if not isinstance(schema, dict): return None
    if "const" in schema and not _schema_same(schema["const"], value):
        return "%s must be %s" % (at, json.dumps(schema["const"]))
    if isinstance(schema.get("enum"), list) and not any(_schema_same(o, value) for o in schema["enum"]):
        return "%s must be one of %s" % (at, json.dumps(schema["enum"]))
    kinds = schema.get("type")
    kinds = [] if kinds is None else (kinds if isinstance(kinds, list) else [kinds])
    if kinds and not any(_schema_type(value, k) for k in kinds):
        return "%s must be %s" % (at, " or ".join(str(k) for k in kinds))
    def num(key):
        v = schema.get(key)
        return v if isinstance(v, (int, float)) and not isinstance(v, bool) else None
    if isinstance(value, str):
        if num("minLength") is not None and len(value) < num("minLength"):
            return "%s must be at least %s characters" % (at, schema["minLength"])
        if num("maxLength") is not None and len(value) > num("maxLength"):
            return "%s must be at most %s characters" % (at, schema["maxLength"])
        if isinstance(schema.get("pattern"), str):
            try:
                pattern = re.compile(schema["pattern"])
            except re.error:
                pattern = None
            if pattern is not None and not pattern.search(value):
                return "%s must match %s" % (at, schema["pattern"])
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        if num("minimum") is not None and value < num("minimum"): return "%s must be >= %s" % (at, schema["minimum"])
        if num("maximum") is not None and value > num("maximum"): return "%s must be <= %s" % (at, schema["maximum"])
        if num("exclusiveMinimum") is not None and value <= num("exclusiveMinimum"): return "%s must be > %s" % (at, schema["exclusiveMinimum"])
        if num("exclusiveMaximum") is not None and value >= num("exclusiveMaximum"): return "%s must be < %s" % (at, schema["exclusiveMaximum"])
    if isinstance(value, list):
        if num("minItems") is not None and len(value) < num("minItems"): return "%s must have at least %s items" % (at, schema["minItems"])
        if num("maxItems") is not None and len(value) > num("maxItems"): return "%s must have at most %s items" % (at, schema["maxItems"])
        if isinstance(schema.get("items"), dict):
            for i, item in enumerate(value):
                err = _schema_error(schema["items"], item, "%s[%d]" % (at, i))
                if err: return err
    if isinstance(value, dict):
        properties = schema.get("properties") if isinstance(schema.get("properties"), dict) else {}
        for name in schema.get("required") or []:
            if name not in value: return "%s.%s is required" % (at, name)
        extra = schema.get("additionalProperties")
        for name, item in value.items():
            if name in properties:
                err = _schema_error(properties[name], item, "%s.%s" % (at, name))
            elif extra is False:
                err = "%s.%s is not allowed" % (at, name)
            elif isinstance(extra, dict):
                err = _schema_error(extra, item, "%s.%s" % (at, name))
            else:
                err = None
            if err: return err
    return None

def _input_problem(request):
    if _input_schema is None or not isinstance(request, dict):
        return None
    headers = request.get("headers") or {}
    marked = False
    for key, value in headers.items():
        if str(key).lower() == "x-gregale-function-call":
            marked = "1" in (value if isinstance(value, list) else [value])
    if not marked:
        return None
    raw = base64.b64decode(request.get("body_b64", "") or "").decode("utf-8", errors="replace")
    detail = None
    if raw.strip() == "":
        detail = _schema_error(_input_schema, None, "input")
    else:
        try:
            detail = _schema_error(_input_schema, json.loads(raw), "input")
        except ValueError:
            detail = "input must be JSON"
    if detail is None:
        return None
    docs = "https://gregale.dev/docs/errors#function_input_invalid"
    body = json.dumps({"type": docs, "title": "Function input invalid", "status": 422,
                       "code": "function_input_invalid", "detail": detail, "docs_url": docs})
    return {"status": 422, "headers": {"content-type": "application/problem+json"},
            "body_b64": base64.b64encode(body.encode("utf-8")).decode("ascii")}
`
