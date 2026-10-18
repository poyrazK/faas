package clientgen

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/dataapi"
)

var pythonKeywords = map[string]bool{"False": true, "None": true, "True": true, "and": true, "as": true, "assert": true, "async": true, "await": true, "break": true, "class": true, "continue": true, "def": true, "del": true, "elif": true, "else": true, "except": true, "finally": true, "for": true, "from": true, "global": true, "if": true, "import": true, "in": true, "is": true, "lambda": true, "nonlocal": true, "not": true, "or": true, "pass": true, "raise": true, "return": true, "try": true, "while": true, "with": true, "yield": true}

type pyRenderer struct {
	m model
	b strings.Builder
}

func (p *pyRenderer) printf(format string, args ...any) { fmt.Fprintf(&p.b, format, args...) }

func pyString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func (p *pyRenderer) pyType(t Type) string {
	var base string
	switch t.Kind {
	case KindString:
		base = "str"
	case KindNumber:
		base = "float"
	case KindBoolean:
		base = "bool"
	case KindEnum:
		if base = p.m.enumName(t); base == "" {
			quoted := make([]string, len(t.Literals))
			for i, l := range t.Literals {
				quoted[i] = pyString(l)
			}
			base = "Literal[" + strings.Join(quoted, ", ") + "]"
		}
	case KindArray:
		base = "List[" + p.pyType(*t.Elem) + "]"
	case KindRow:
		if def, ok := p.m.tableByKey[t.Schema+"."+t.Name]; ok {
			base = pyString(def.Base + "Row")
		} else {
			base = "Json"
		}
	case KindComposite:
		if name := p.m.compositeNm[t.Schema+"."+t.Name]; name != "" {
			base = pyString(name)
		} else {
			base = "Json"
		}
	case KindVoid:
		return "None"
	default:
		return "Json"
	}
	if t.Nullable {
		return "Optional[" + base + "]"
	}
	return base
}

// typedDict uses the functional form so any column name is a valid key.
func (p *pyRenderer) typedDict(name string, fields [][2]string, total bool) {
	p.printf("%s = TypedDict(%s, {", name, pyString(name))
	for i, f := range fields {
		if i > 0 {
			p.printf(", ")
		}
		p.printf("%s: %s", pyString(f[0]), f[1])
	}
	if total {
		p.printf("})\n")
	} else {
		p.printf("}, total=False)\n")
	}
}

func (p *pyRenderer) column(c dataapi.Column) string {
	t := ParseType(c.Type)
	t.Nullable = t.Nullable || c.Nullable
	return p.pyType(t)
}

func pyIdentifier(value string) string {
	name := snake(value)
	if pythonKeywords[name] {
		name += "_"
	}
	return name
}

// RenderPython renders a standard-library-only Python 3.9+ client module.
func RenderPython(contract dataapi.Contract) ([]byte, error) {
	p := &pyRenderer{m: buildModel(contract)}
	p.b.WriteString(header("#", p.m.Fingerprint))
	p.printf("from __future__ import annotations\n\nimport json\nimport urllib.error\nimport urllib.parse\nimport urllib.request\nfrom typing import Any, Callable, Dict, Generic, List, Literal, Optional, Sequence, TypedDict, TypeVar, Union\n\n")
	p.printf("SCHEMA_FINGERPRINT = %s\n\nJson = Any\n\n", pyString(p.m.Fingerprint))
	for _, e := range p.m.Enums {
		quoted := make([]string, len(e.Labels))
		for i, l := range e.Labels {
			quoted[i] = pyString(l)
		}
		p.printf("# PostgreSQL enum %s\n%s = Literal[%s]\n", e.Key, e.Name, strings.Join(quoted, ", "))
	}
	for _, c := range p.m.Composites {
		fields := make([][2]string, 0, len(c.Fields))
		for _, f := range c.Fields {
			t := ParseType(f.Type)
			t.Nullable = t.Nullable || f.Nullable
			fields = append(fields, [2]string{f.Name, p.pyType(t)})
		}
		p.typedDict(p.m.compositeNm[c.Schema+"."+c.Name], fields, true)
	}
	for _, t := range p.m.Tables {
		var row, required, optional, update [][2]string
		for _, c := range t.Columns {
			row = append(row, [2]string{c.Name, p.column(c)})
			if c.Insertable && c.Optional {
				optional = append(optional, [2]string{c.Name, p.column(c)})
			} else if c.Insertable {
				required = append(required, [2]string{c.Name, p.column(c)})
			}
			if c.Updatable {
				update = append(update, [2]string{c.Name, p.column(c)})
			}
		}
		p.printf("\n# %s.%s\n", t.Schema, t.Name)
		p.typedDict(t.Base+"Row", row, true)
		// Required and optional insert keys combine through TypedDict inheritance.
		p.typedDict("_"+t.Base+"InsertRequired", required, true)
		p.typedDict("_"+t.Base+"InsertOptional", optional, false)
		p.printf("\n\nclass %sInsert(_%sInsertRequired, _%sInsertOptional):\n    pass\n\n\n", t.Base, t.Base, t.Base)
		p.typedDict(t.Base+"Update", update, false)
	}
	for _, f := range p.m.Functions {
		var required, optional [][2]string
		for _, a := range f.Args {
			if a.Optional {
				optional = append(optional, [2]string{a.Name, p.pyType(ParseType(a.Type))})
			} else {
				required = append(required, [2]string{a.Name, p.pyType(ParseType(a.Type))})
			}
		}
		p.printf("\n# %s.%s arguments\n", f.Schema, f.Name)
		p.typedDict("_"+f.Base+"Required", required, true)
		p.typedDict("_"+f.Base+"Optional", optional, false)
		p.printf("\n\nclass %s(_%sRequired, _%sOptional):\n    pass\n", f.Base, f.Base, f.Base)
	}
	p.b.WriteString(pythonRuntime)
	methods := newNames("rpc", "table")
	for _, t := range p.m.Tables {
		p.printf("\n    def %s(self) -> \"Table[%sRow, %sInsert, %sUpdate]\":\n        \"\"\"Query %s.%s.\"\"\"\n        return Table(self, %s, %s)\n",
			methods.take(pyIdentifier(qualified(p.m.Schemas, t.Schema, t.Name))), t.Base, t.Base, t.Base, t.Schema, t.Name, pyString(t.Schema), pyString(t.Name))
	}
	for _, f := range p.m.Functions {
		p.printf("\n    def %s(self, args: %s) -> %s:\n        \"\"\"Call %s.%s.\"\"\"\n        return self.rpc(%s, args, schema=%s)\n",
			methods.take("rpc_"+pyIdentifier(qualified(p.m.Schemas, f.Schema, f.Name))), f.Base, p.pyType(f.Returns), f.Schema, f.Name, pyString(f.Name), pyString(f.Schema))
	}
	return []byte(p.b.String()), nil
}

// pythonRuntime never retries writes: a lost response can follow a committed
// write, so callers reconcile before replaying.
const pythonRuntime = `

R = TypeVar("R")
I = TypeVar("I")
U = TypeVar("U")


class DataAPIError(Exception):
    """A Data API or database error with the stable code when one was returned."""

    def __init__(self, status: int, code: str, message: str, request_id: str) -> None:
        super().__init__(f"data api: HTTP {status} {code}: {message} (request {request_id})")
        self.status = status
        self.code = code
        self.message = message
        self.request_id = request_id


def _format(value: Any) -> str:
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "true" if value else "false"
    return str(value)


class Table(Generic[R, I, U]):
    """Builds a query on one relation; each method returns a new Table."""

    def __init__(self, client: "DataClient", schema: str, name: str, params: Optional[List[tuple]] = None) -> None:
        self._client = client
        self._schema = schema
        self._name = name
        self._params = list(params or [])

    def _with(self, key: str, value: str, replace: bool = False) -> "Table[R, I, U]":
        params = [p for p in self._params if not (replace and p[0] == key)]
        params.append((key, value))
        return Table(self._client, self._schema, self._name, params)

    def select(self, columns: str = "*") -> "Table[R, I, U]":
        return self._with("select", columns, replace=True)

    def _filter(self, column: str, operator: str, value: Any) -> "Table[R, I, U]":
        return self._with(column, f"{operator}.{_format(value)}")

    def eq(self, column: str, value: Any) -> "Table[R, I, U]":
        return self._filter(column, "eq", value)

    def neq(self, column: str, value: Any) -> "Table[R, I, U]":
        return self._filter(column, "neq", value)

    def gt(self, column: str, value: Any) -> "Table[R, I, U]":
        return self._filter(column, "gt", value)

    def gte(self, column: str, value: Any) -> "Table[R, I, U]":
        return self._filter(column, "gte", value)

    def lt(self, column: str, value: Any) -> "Table[R, I, U]":
        return self._filter(column, "lt", value)

    def lte(self, column: str, value: Any) -> "Table[R, I, U]":
        return self._filter(column, "lte", value)

    def like(self, column: str, pattern: str) -> "Table[R, I, U]":
        return self._filter(column, "like", pattern)

    def ilike(self, column: str, pattern: str) -> "Table[R, I, U]":
        return self._filter(column, "ilike", pattern)

    def is_(self, column: str, value: Optional[bool]) -> "Table[R, I, U]":
        return self._filter(column, "is", value)

    def in_(self, column: str, values: Sequence[Any]) -> "Table[R, I, U]":
        return self._with(column, "in.(" + ",".join(json.dumps(_format(v)) for v in values) + ")")

    def order(self, column: str, descending: bool = False) -> "Table[R, I, U]":
        item = f"{column}.{'desc' if descending else 'asc'}"
        current = [v for k, v in self._params if k == "order"]
        return self._with("order", ",".join(current + [item]), replace=True)

    def limit(self, count: int) -> "Table[R, I, U]":
        return self._with("limit", str(int(count)), replace=True)

    def offset(self, count: int) -> "Table[R, I, U]":
        return self._with("offset", str(int(count)), replace=True)

    def get(self) -> List[R]:
        """Read matching rows. The server caps one response at 1,000 rows."""
        return self._client._request("GET", self._schema, "/rest/v1/" + self._name, self._params)

    def insert(self, rows: Union[I, Sequence[I]]) -> List[R]:
        """Insert rows in one transaction and return them as stored."""
        body = list(rows) if isinstance(rows, (list, tuple)) else [rows]
        return self._client._request("POST", self._schema, "/rest/v1/" + self._name, self._params, body)

    def _filtered(self) -> bool:
        return any(k not in ("select", "order", "limit", "offset") for k, _ in self._params)

    def update(self, values: U) -> List[R]:
        """Update matching rows; refuses to run without a filter."""
        if not self._filtered():
            raise ValueError(f"update of {self._schema}.{self._name} requires a filter")
        return self._client._request("PATCH", self._schema, "/rest/v1/" + self._name, self._params, values)

    def delete(self) -> List[R]:
        """Delete matching rows; refuses to run without a filter."""
        if not self._filtered():
            raise ValueError(f"delete from {self._schema}.{self._name} requires a filter")
        return self._client._request("DELETE", self._schema, "/rest/v1/" + self._name, self._params)


class DataClient:
    """Calls one Gregale Data API.

    Pass access_token (a string or a callable returning one) for application
    users, or server_key for trusted backends. Never ship a server key to a
    browser. subject lets a server key act for one user; RLS still applies.
    """

    def __init__(self, base_url: str, *, access_token: Union[str, Callable[[], str], None] = None,
                 server_key: Optional[str] = None, subject: Optional[str] = None, timeout: float = 20.0) -> None:
        self.base_url = base_url.rstrip("/")
        self.access_token = access_token
        self.server_key = server_key
        self.subject = subject
        self.timeout = timeout

    def table(self, name: str, schema: str = "api") -> Table[Dict[str, Any], Dict[str, Any], Dict[str, Any]]:
        """Query a relation without generated types."""
        return Table(self, schema, name)

    def rpc(self, name: str, args: Optional[Dict[str, Any]] = None, *, schema: str = "api") -> Any:
        """Call an approved RPC function."""
        return self._request("POST", schema, "/rest/v1/rpc/" + name, [], args or {})

    def _request(self, method: str, schema: str, path: str, params: List[tuple], body: Any = None) -> Any:
        url = self.base_url + path
        if params:
            url += "?" + urllib.parse.urlencode(params)
        headers = {"Accept": "application/json"}
        if method == "GET":
            headers["Accept-Profile"] = schema
        else:
            headers["Content-Profile"] = schema
            headers["Content-Type"] = "application/json"
            headers["Prefer"] = "return=representation"
        if self.server_key:
            headers["Authorization"] = "Bearer " + self.server_key
            if self.subject:
                headers["X-Gregale-Subject"] = self.subject
        elif self.access_token is not None:
            token = self.access_token() if callable(self.access_token) else self.access_token
            headers["Authorization"] = "Bearer " + token
        data = None if body is None or method == "GET" else json.dumps(body).encode()
        request = urllib.request.Request(url, data=data, method=method, headers=headers)
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                payload = response.read(16 * 1024 * 1024 + 1)
        except urllib.error.HTTPError as error:
            raw = error.read(64 * 1024)
            try:
                problem = json.loads(raw or b"{}")
            except ValueError:
                problem = {}
            if not isinstance(problem, dict):
                problem = {}
            raise DataAPIError(error.code, str(problem.get("code") or ""), str(problem.get("message") or problem.get("title") or ""),
                               error.headers.get("X-Request-Id") or "") from None
        return json.loads(payload) if payload.strip() else None
`
