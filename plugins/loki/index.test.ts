import assert from "node:assert/strict";
import { test } from "node:test";
import { entries, since } from "./index.ts";

test("since parses the time window", () => {
  assert.deepEqual(since('since 6h {app="x"}'), { query: '{app="x"}', seconds: 21600 });
  assert.deepEqual(since('{app="x"}'), { query: '{app="x"}', seconds: 3600 });
});

test("entries merges streams in time order", () => {
  const es = entries({
    resultType: "streams",
    result: [
      { stream: { app: "a" }, values: [["3", "c"], ["1", "a"]] },
      { stream: { app: "b" }, values: [["2", "b"]] },
    ],
  });
  assert.deepEqual(es.map((e) => e.line), ["a", "b", "c"]);
  assert.equal(es[1].labels, '{app="b"}');
  assert.throws(() => entries({ resultType: "vector", result: [] }), /metric/);
});
