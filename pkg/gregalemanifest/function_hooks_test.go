package gregalemanifest

import (
	"slices"
	"testing"
)

func TestDetectFunctionHooks(t *testing.T) {
	for _, tc := range []struct {
		name, file, src string
		want            []string
	}{
		{"default object methods", "handler.js", `export default {
  async init() { await load(); },
  beforeSnapshot() {},
  async afterRestore(ctx) { pool.reset(); },
  async fetch(req) { return new Response("ok"); },
};`, []string{"init", "before_snapshot", "after_restore"}},
		{"shorthand and properties", "handler.mjs", `async function afterRestore() {}
const init = async () => {};
export default { fetch, init, afterRestore: afterRestore };`, []string{"init", "after_restore"}},
		{"named exports", "handler.js", `export async function handler(event) { return {}; }
export function beforeSnapshot() {}
export const afterRestore = async () => {};`, []string{"before_snapshot", "after_restore"}},
		{"nested members are not hooks", "handler.js", `export default {
  async fetch(req) { const x = { init() {}, afterRestore: 1 }; init(); return new Response(String(x)); },
  // afterRestore() {}
  name: "afterRestore()",
};`, nil},
		{"jsdoc typed export", "handler.js", `/** @type {import('@gregale/sdk-node').FunctionSnapshotHooks & {fetch(request: Request): Promise<Response>}} */
export default {
  /** @param {import('@gregale/sdk-node').FunctionHookContext} ctx */
  async beforeSnapshot(ctx) {},
  async fetch(request) { return new Response('ok'); },
};`, []string{"before_snapshot"}},
		{"no hooks", "handler.js",`export default { async fetch() { return new Response("ok"); } };`, nil},
		{"python module functions", "handler.py", `import asyncio

async def init():
    pass

def after_restore(ctx):
    pass

def handler(event, ctx):
    def init():  # nested, not module level
        pass
    return {}
`, []string{"init", "after_restore"}},
		{"python indented is not module level", "handler.py", `class X:
    def before_snapshot(self):
        pass

def handler(event, ctx):
    return {}
`, nil},
		{"go is not read", "main.go", `func init() {}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectFunctionHooks(tc.file, []byte(tc.src)); !slices.Equal(got, tc.want) {
				t.Fatalf("hooks = %v, want %v", got, tc.want)
			}
		})
	}
}
