import { createWriteCaller } from "./server.js";
createWriteCaller({ deadlineMS: Number(process.env.CHECKOUT_DEADLINE_MS || 5000), dependencyURL: process.env.PAYMENT_URL || "http://payment.svc.gregale:10080" })
  .listen(Number(process.env.PORT || 8080), process.env.HOST || "0.0.0.0");
