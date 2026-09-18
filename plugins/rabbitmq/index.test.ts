import assert from "node:assert/strict";
import { test } from "node:test";
import { endpoints } from "./index.ts";

test("endpoints derive the management API", () => {
  const ep = endpoints("amqp://app:s%3Acret@mq:5672/prod?management=https://mq.admin:443/&heartbeat=5");
  assert.equal(ep.amqp, "amqp://app:s%3Acret@mq:5672/prod?heartbeat=5");
  assert.equal(ep.api, "https://mq.admin:443");
  assert.equal(ep.vhost, "prod");
  assert.equal(ep.auth, "Basic " + Buffer.from("app:s:cret").toString("base64"));
  assert.equal(endpoints("amqp://localhost").vhost, "/");
  assert.equal(endpoints("amqp://localhost").api, "http://localhost:15672");
});
