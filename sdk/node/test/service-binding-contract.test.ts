import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';
import { AppsService, OpenAPI } from '../src/preview.js';
import type { AppResponse, CreateAppRequest, UpdateAppRequest } from '../src/preview.js';

type Schema = {
  $ref?: string;
  properties?: Record<string, Schema>;
  required?: string[];
  enum?: string[];
  additionalProperties?: Schema;
};
const contracts = new URL('../../../service-binding-contracts/', import.meta.url);
const { schemas } = JSON.parse(readFileSync(new URL('schema.json', contracts), 'utf8')) as {
  schemas: Record<string, Schema>;
};
const cases = JSON.parse(readFileSync(new URL('cases.json', contracts), 'utf8')) as {
  requests: { name: string; schema: string; body: Record<string, unknown> }[];
  responses: { name: string; body: AppResponse }[];
};

function references(node: ts.Node): string[] {
  const names: string[] = [];
  if (ts.isTypeReferenceNode(node)) names.push(node.typeName.getText());
  node.forEachChild(child => { names.push(...references(child)); });
  return names;
}

test('generated models declare the shared OpenAPI binding fields and enums', () => {
  for (const [name, schema] of Object.entries(schemas)) {
    const source = ts.createSourceFile(name, readFileSync(
      new URL(`../../src/generated/models/${name}.ts`, import.meta.url), 'utf8',
    ), ts.ScriptTarget.Latest, true);
    const alias = source.statements.find((node): node is ts.TypeAliasDeclaration =>
      ts.isTypeAliasDeclaration(node) && node.name.text === name);
    assert.ok(alias, `declare model ${name}`);
    if (schema.properties) {
      assert.ok(ts.isTypeLiteralNode(alias.type), `${name} must declare typed fields`);
      const fields = new Map(alias.type.members.filter(ts.isPropertySignature).map(field =>
        [field.name.getText().replace(/['"]/g, ''), field]));
      for (const [key, prop] of Object.entries(schema.properties)) {
        const field = fields.get(key);
        assert.ok(field, `${name} must declare ${key}`);
        assert.equal(!field.questionToken, schema.required?.includes(key) ?? false, `${name}.${key} required`);
        if (prop.$ref) {
          assert.ok(references(field).includes(prop.$ref.split('/').at(-1)!), `${name}.${key} referenced type`);
        }
      }
    }
    if (schema.enum) {
      const members = ts.isUnionTypeNode(alias.type) ? alias.type.types : [alias.type];
      const values = members.map(node => {
        assert.ok(ts.isLiteralTypeNode(node) && ts.isStringLiteral(node.literal), `${name} string enum`);
        return node.literal.text;
      });
      assert.deepEqual(values, schema.enum, `${name} enum values`);
    }
    if (schema.additionalProperties?.$ref) {
      assert.ok(references(alias.type).includes(schema.additionalProperties.$ref.split('/').at(-1)!), `${name} map value type`);
    }
  }
});

function project(schema: Schema, value: unknown): unknown {
  if (value === null || value === undefined) return value;
  if (schema.$ref) return project(schemas[schema.$ref.split('/').at(-1)!]!, value);
  if (schema.properties) {
    const object = value as Record<string, unknown>;
    return Object.fromEntries(Object.entries(schema.properties)
      .filter(([key]) => Object.hasOwn(object, key))
      .map(([key, prop]) => [key, project(prop, object[key])]));
  }
  return value;
}

test('shared binding fixtures survive create, PATCH and app response transport', async t => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.invalid';
  try {
    for (const fixture of cases.requests) {
      await t.test(fixture.name, async () => {
        const create = fixture.schema === 'CreateAppRequest';
        assert.ok(create || fixture.schema === 'UpdateAppRequest');
        let calls = 0;
        globalThis.fetch = async (input, init) => {
          calls++;
          assert.equal(new URL(String(input)).pathname, create ? '/v1/apps' : '/v1/apps/demo');
          assert.equal(init?.method, create ? 'POST' : 'PATCH');
          assert.deepEqual(project(schemas[fixture.schema]!, JSON.parse(String(init?.body))),
            project(schemas[fixture.schema]!, fixture.body));
          return Response.json(cases.responses[1]!.body, { status: create ? 201 : 200 });
        };
        const app = create
          ? await AppsService.createApp({ requestBody: fixture.body as CreateAppRequest })
          : await AppsService.updateApp({ slug: 'demo', requestBody: fixture.body as UpdateAppRequest });
        assert.equal(calls, 1);
        assert.equal(app.manifest.service_binding_transport, 'https');
      });
    }
    for (const fixture of cases.responses) {
      await t.test(`response/${fixture.name}`, async () => {
        globalThis.fetch = async (input, init) => {
          assert.equal(new URL(String(input)).pathname, '/v1/apps/demo');
          assert.equal(init?.method, 'GET');
          return Response.json(fixture.body);
        };
        const app = await AppsService.getApp({ slug: 'demo' });
        assert.deepEqual(project(schemas.AppResponse!, app), project(schemas.AppResponse!, fixture.body));
      });
    }
  } finally {
    globalThis.fetch = oldFetch;
    OpenAPI.BASE = oldBase;
  }
});
