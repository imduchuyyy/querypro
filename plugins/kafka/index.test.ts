import assert from "node:assert/strict";
import { test } from "node:test";
import { parse } from "./index.ts";

test("parse brokers, tls and sasl", () => {
  assert.deepEqual(parse("localhost:9092"), { brokers: ["localhost:9092"], ssl: false, sasl: undefined });
  assert.deepEqual(parse("kafka://u:p%40ss:x@a:9092,b:9092/?ssl=true&mechanism=scram-sha-512"), {
    brokers: ["a:9092", "b:9092"],
    ssl: true,
    sasl: { mechanism: "scram-sha-512", username: "u", password: "p@ss:x" },
  });
  assert.throws(() => parse("kafka://"), /no brokers/);
  assert.throws(() => parse("kafka://u:p@a:1?mechanism=gssapi"), /mechanism/);
});
