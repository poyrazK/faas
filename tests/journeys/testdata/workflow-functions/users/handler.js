export default async function handler(event) {
  return { statusCode: 200, body: { hello: event.body.email, probe: "gregale-e2e-users" } };
}
