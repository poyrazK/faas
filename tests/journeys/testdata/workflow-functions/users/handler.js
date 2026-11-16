export default async function handler(event) {
  // A health probe carries no body; answer it instead of throwing.
  return { statusCode: 200, body: { hello: event.body?.email ?? null, probe: "gregale-e2e-users" } };
}
