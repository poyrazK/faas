export const dynamic = "force-dynamic";

// The journey checks where the server runs from: the standalone tree.
export function GET() {
  return Response.json({ cwd: process.cwd(), node: process.versions.node });
}
