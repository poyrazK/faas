import { openPostgresLedger } from "./ledger.js";
import { createPostgresProvider } from "./server.js";
try {
  if (!process.env.DATABASE_URL || process.env.MIGRATION_DATABASE_URL || process.env.LEDGER_TEST_DISPOSABLE !== "1") throw new Error("invalid_runtime_configuration");
  if (process.env.GREGALE_NATIVE_COMMIT_LOSS === "1") {
    const { createNativeCommitProvider } = await import("./native-fixture/commit-provider.mjs");
    const fixture = await createNativeCommitProvider({ databaseURL: process.env.DATABASE_URL, namespace: process.env.LEDGER_NAMESPACE,
      disposable: process.env.LEDGER_TEST_DISPOSABLE, cert: process.env.NATIVE_AUTH_CERT_PEM, key: process.env.NATIVE_AUTH_KEY_PEM,
      controlToken: process.env.NATIVE_AUTH_CONTROL_TOKEN, faultMode: process.env.GREGALE_NATIVE_COMMIT_MODE || "drop" });
    fixture.server.listen(Number(process.env.PORT || 8080), process.env.HOST || "0.0.0.0");
  } else if (process.env.GREGALE_NATIVE_AUTH_STALL === "1" || process.env.GREGALE_NATIVE_BANDWIDTH === "1" || process.env.GREGALE_NATIVE_READ_CUT === "1" || process.env.GREGALE_NATIVE_SSL_REQUEST === "1") {
    const { createNativeAuthProvider } = await import("./native-fixture/provider.mjs");
    const fixture = await createNativeAuthProvider({ databaseURL: process.env.DATABASE_URL, namespace: process.env.LEDGER_NAMESPACE,
      disposable: process.env.LEDGER_TEST_DISPOSABLE, cert: process.env.NATIVE_AUTH_CERT_PEM, key: process.env.NATIVE_AUTH_KEY_PEM,
      controlToken: process.env.NATIVE_AUTH_CONTROL_TOKEN, fixtureMode: process.env.GREGALE_NATIVE_SSL_REQUEST === "1" ? "ssl-request" : process.env.GREGALE_NATIVE_READ_CUT === "1" ? "read-cut" : process.env.GREGALE_NATIVE_BANDWIDTH === "1" ? "bandwidth" : "authentication" });
    fixture.server.listen(Number(process.env.PORT || 8080), process.env.HOST || "0.0.0.0");
  } else {
    const ledger = await openPostgresLedger({ connectionString: process.env.DATABASE_URL, namespace: process.env.LEDGER_NAMESPACE });
    createPostgresProvider({ ledger, holdFirstKey: process.env.HOLD_FIRST_KEY || "in-flight", holdStage: process.env.HOLD_STAGE || "pending" })
      .listen(Number(process.env.PORT || 8080), process.env.HOST || "0.0.0.0");
  }
} catch { console.error("Postgres provider startup failed; check runtime binding and migrated schema"); process.exitCode = 1; }
