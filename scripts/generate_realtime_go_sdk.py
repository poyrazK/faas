import json
import re
import subprocess
from pathlib import Path

from ruamel.yaml import YAML

r = Path(__file__).resolve().parents[1]
spec = YAML(typ="safe").load((r / "api/openapi.yaml").read_text())
schemas = spec["components"]["schemas"]
mapfile = r / "cmd/sdk-coverage/main.go"
mapsource = mapfile.read_text()
release_keys = set(json.loads((r / "scripts/realtime_go_sdk_routes.json").read_text()))
mapped = set(re.findall(r'"([A-Z]+ /[^"\n]+)"\s*:', mapsource))
existingtypes = set()
existingmethods = set()
for p in (r / "pkg/api").glob("*.go"):
    if p.name.endswith("_test.go") or p.name in (
        "realtime_release_sdk_models.go",
        "client_realtime_release.go",
    ):
        continue
    s = p.read_text()
    existingtypes.update(re.findall(r"(?m)^type\s+(\w+)\s", s))
    existingmethods.update(re.findall(r"func \(c \*Client\) (\w+)\(", s))
initialisms = {
    "id": "ID",
    "ids": "IDs",
    "ttl": "TTL",
    "url": "URL",
    "api": "API",
    "json": "JSON",
    "http": "HTTP",
    "uuid": "UUID",
    "sql": "SQL",
    "sse": "SSE",
}


def name(s):
    return "".join(
        initialisms.get(x.lower(), x[:1].upper() + x[1:])
        for x in re.split(r"[_\- ]+", s)
    )


def varname(s):
    n = name(s)
    return n[:1].lower() + n[1:] if n not in ("ID", "URL") else n.lower()


needed = {}
modeldefs = {}


def typename(schema, hint):
    if "$ref" in schema:
        n = schema["$ref"].split("/")[-1]
        if n not in existingtypes:
            needed[n] = schemas[n]
        return n
    if "allOf" in schema:
        needed.setdefault(hint, schema)
        return hint
    typ = schema.get("type")
    if isinstance(typ, list):
        typ = next((x for x in typ if x != "null"), None)
    if typ == "object" or "properties" in schema:
        if schema.get("properties"):
            needed.setdefault(hint, schema)
            return hint
        additional = schema.get("additionalProperties", True)
        return "map[string]" + (
            typename(additional, hint + "Value")
            if isinstance(additional, dict)
            else "json.RawMessage"
        )
    if typ == "array":
        return "[]" + typename(schema.get("items", {}), hint + "Item")
    if typ == "string":
        return "time.Time" if schema.get("format") == "date-time" else "string"
    if typ == "integer":
        return "int64" if schema.get("format") == "int64" else "int"
    if typ == "number":
        return "float64"
    if typ == "boolean":
        return "bool"
    return "json.RawMessage"


def nullable(schema):
    return isinstance(schema.get("type"), list) and "null" in schema["type"]


def objectfields(schema, n):
    props = dict(schema.get("properties", {}))
    required = set(schema.get("required", []))
    embed = []
    for part in schema.get("allOf", []):
        if "$ref" in part:
            embed.append(typename(part, n + "Base"))
        else:
            props.update(part.get("properties", {}))
            required.update(part.get("required", []))
    lines = [f" {e}\n" for e in embed]
    for key, value in props.items():
        t = typename(value, n + name(key))
        optional = key not in required
        if (optional or nullable(value)) and not t.startswith(
            ("map[", "[]", "json.RawMessage", "*")
        ):
            t = "*" + t
        tag = key + (",omitempty" if optional else "")
        lines.append(f' {name(key)} {t} `json:"{tag}"`\n')
    return "".join(lines)


client = """// Code generated from the managed realtime OpenAPI surface. DO NOT EDIT.
package api

import (
 "context"
 "fmt"
 "io"
 "net/http"
 "net/url"
 "strings"
)

// realtimeReleasePath escapes every path value before expanding the contract template.
func realtimeReleasePath(template string, values map[string]string) string {
 for key, value := range values { template = strings.ReplaceAll(template, "{"+key+"}", url.PathEscape(value)) }
 return template
}

// doRealtimeRelease keeps scoped worker/publisher credentials on their intended origin.
func (c *Client) doRealtimeRelease(ctx context.Context, method, path string, body, out any, headers http.Header) error {
 if !strings.HasPrefix(path, "/v1/realtime/") { return c.doWithClientAndHeadersAndIdempotencyKey(ctx, c.http, method, path, body, out, headers.Get("Idempotency-Key"), headers) }
 clone := *c.http
 clone.Jar = nil
 clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
 return c.doWithClientAndHeadersAndIdempotencyKey(ctx, &clone, method, path, body, out, headers.Get("Idempotency-Key"), headers)
}

// streamRealtimeRelease writes bounded-server JSONL exports without JSON decoding or buffering.
func (c *Client) streamRealtimeRelease(ctx context.Context, path string, dst io.Writer, headers http.Header) (int64,error) {
 if dst == nil { return 0, fmt.Errorf("export destination is required") }
 req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
 if err != nil { return 0, err }
 if c.token != "" { req.Header.Set("Authorization", "Bearer "+c.token) }
 for key,values := range headers { req.Header[key] = values }
 clone := *c.http
 clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
 resp, err := clone.Do(req)
 if err != nil { return 0, fmt.Errorf("could not reach the API: %w",err) }
 defer func(){ _ = resp.Body.Close() }()
 if resp.StatusCode < 200 || resp.StatusCode >=300 {
  data, err := readBoundedResponse(resp,maxResponseBodyBytes)
  if err != nil { return 0,err }
  return 0,apiErrorFromResponse(resp,data)
 }
 return io.Copy(dst,resp.Body)
}
"""
options = ""
routes = []
newmethods = 0
for path, item in spec["paths"].items():
    if "/realtime/" not in path and not path.endswith("/realtime-channel"):
        continue
    for method, op in item.items():
        if method not in ("get", "put", "post", "delete", "patch"):
            continue
        key = method.upper() + " " + path
        if key not in release_keys:
            continue
        operation = op.get("operationId")
        assert operation, (method, path)
        methodname = operation[0].upper() + operation[1:]
        routes.append((key, methodname))
        if methodname in existingmethods:
            continue
        params = item.get("parameters", []) + op.get("parameters", [])
        params = [
            spec["components"]["parameters"][p["$ref"].split("/")[-1]]
            if "$ref" in p
            else p
            for p in params
        ]
        pathnames = re.findall(r"\{([^}]+)\}", path)
        args = ["ctx context.Context"] + [varname(p) + " string" for p in pathnames]
        bodySchema = (
            op.get("requestBody", {})
            .get("content", {})
            .get("application/json", {})
            .get("schema")
        )
        body = "nil"
        if bodySchema:
            bt = typename(bodySchema, methodname + "Request")
            args.append("request " + bt)
            body = "request"
        extra = [p for p in params if p["in"] in ("query", "header")]
        setup = " headers := http.Header{}\n"
        if extra:
            optname = methodname + "Options"
            args.append("options *" + optname)
            options += f"// {optname} carries optional query and header parameters.\ntype {optname} struct {{\n"
            setup += " query := url.Values{}\n if options != nil {\n"
            for p in extra:
                field = name(p["name"])
                t = typename(p.get("schema", {}), optname + field)
                options += f" {field} *{t}\n"
                value = "*options." + field
                if p.get("schema", {}).get("format") == "date-time":
                    value = (
                        "options."
                        + field
                        + '.Format("2006-01-02T15:04:05.999999999Z07:00")'
                    )
                target = "headers" if p["in"] == "header" else "query"
                if t.startswith("[]"):
                    if p["in"] == "query" and p.get("explode", True):
                        setup += f' if options.{field} != nil {{ for _, value := range *options.{field} {{ query.Add("{p["name"]}",fmt.Sprint(value)) }} }}\n'
                    else:
                        setup += f' if options.{field} != nil {{ values := make([]string,0,len(*options.{field})); for _, value := range *options.{field} {{ values = append(values,fmt.Sprint(value)) }}; {target}.Set("{p["name"]}",strings.Join(values,",")) }}\n'
                else:
                    setup += f' if options.{field} != nil {{ {target}.Set("{p["name"]}",fmt.Sprint({value})) }}\n'
            setup += ' }\n if encoded := query.Encode(); encoded != "" { path += "?"+encoded }\n'
            options += "}\n\n"
        required = [p for p in extra if p.get("required")]
        # Success payload: handle JSON, streaming exports and no-content responses distinctly.
        success = next(
            (v for k, v in op.get("responses", {}).items() if str(k).startswith("2")),
            {},
        )
        content = success.get("content", {})
        js = content.get("application/json", {}).get("schema")
        streaming = any(
            k in content for k in ("application/x-ndjson", "application/octet-stream")
        )
        returns = (
            "(int64,error)"
            if streaming
            else "(" + typename(js, methodname + "Response") + ",error)"
            if js
            else "error"
        )
        if streaming:
            args.append("dst io.Writer")
        routeargs = ", ".join('"' + p + '":' + varname(p) for p in pathnames)
        client += f"// {methodname} calls {key}.\n"
        if path.startswith("/v1/realtime/"):
            client += "// Configure a separate Client with the endpoint/group scoped credential.\n"
        client += (
            f"func (c *Client) {methodname}("
            + ", ".join(args)
            + ") "
            + returns
            + " {\n"
        )
        client += (
            f' path := realtimeReleasePath("{path}",map[string]string{{{routeargs}}})\n'
            + setup
        )
        if required:
            for param in required:
                collection = "headers" if param["in"] == "header" else "query"
                condition = collection + '.Get("' + param["name"] + '") == ""'
                message = 'fmt.Errorf("' + param["name"] + ' is required")'
                if streaming:
                    failure = "return 0," + message
                elif js:
                    failure = (
                        "var zero "
                        + typename(js, methodname + "Response")
                        + "; return zero,"
                        + message
                    )
                else:
                    failure = "return " + message
                client += " if " + condition + " { " + failure + " }\n"
        if streaming:
            client += " return c.streamRealtimeRelease(ctx,path,dst,headers)\n"
        elif js:
            rt = typename(js, methodname + "Response")
            client += f' var out {rt}\n err := c.doRealtimeRelease(ctx,"{method.upper()}",path,{body},&out,headers)\n return out,err\n'
        else:
            client += f' return c.doRealtimeRelease(ctx,"{method.upper()}",path,{body},nil,headers)\n'
        client += "}\n\n"
        newmethods += 1
# JSONL records and webhook envelopes are public wire contracts outside JSON responses.
for schema_name in (
    "RealtimeDeadLetterExportManifest",
    "RealtimeDeadLetterExportRecord",
    "RealtimeReplayJobWebhookPayload",
):
    needed[schema_name] = spec["components"]["schemas"][schema_name]
while needed:
    n, s = needed.popitem()
    if n in modeldefs or n in existingtypes:
        continue
    if s.get("type") == "object" or "properties" in s or "allOf" in s:
        modeldefs[n] = "type " + n + " struct {\n" + objectfields(s, n) + "}\n"
    else:
        modeldefs[n] = (
            "type "
            + n
            + " "
            + typename({k: v for k, v in s.items() if k != "$ref"}, n + "Value")
            + "\n"
        )
models = (
    '// Code generated from the managed realtime OpenAPI schemas. DO NOT EDIT.\npackage api\n\nimport ("encoding/json"; "time")\n\n'
    + "\n".join(modeldefs.values())
)
# imports only when used by emitted types
if "json." not in models:
    models = models.replace('"encoding/json"; ', "")
if "time." not in models:
    models = models.replace('; "time"', "").replace('"time"', "")
client = client.replace(' "strings"\n', ' "strings"\n "time"\n') + options
(r / "pkg/api/client_realtime_release.go").write_text(client)
(r / "pkg/api/realtime_release_sdk_models.go").write_text(models)
entries = "\n".join(f' "{k}": "{v}",' for k, v in routes)
mapsource = re.sub(
    r"(?ms)^\s*// BEGIN managed realtime SDK routes\n.*?^\s*// END managed realtime SDK routes\n",
    "",
    mapsource,
)
mapsource = mapsource.replace(
    "var methodRouteMap = map[string]string{",
    "var methodRouteMap = map[string]string{\n // BEGIN managed realtime SDK routes\n"
    + entries
    + "\n // END managed realtime SDK routes",
    1,
)
mapfile.write_text(mapsource)
print(
    "Added",
    newmethods,
    "real Go SDK methods,",
    len(modeldefs),
    "schema types,",
    len(routes),
    "route mappings",
)

subprocess.run(
    [
        "gofmt",
        "-w",
        str(r / "pkg/api/client_realtime_release.go"),
        str(r / "pkg/api/realtime_release_sdk_models.go"),
        str(mapfile),
    ],
    check=True,
)
