import { createNativeAuthCaller } from "./server.js";
createNativeAuthCaller({ dependencyURL: process.env.PAYMENT_URL || "http://payment.svc.gregale:10080" })
  .listen(Number(process.env.PORT || 8080), process.env.HOST || "0.0.0.0");
