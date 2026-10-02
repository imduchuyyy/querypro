import assert from "node:assert/strict";
import { test } from "node:test";
import { format } from "./index.ts";

test("format mirrors redis-cli", () => {
  assert.equal(format(null), "(nil)");
  assert.equal(format(3), "(integer) 3");
  assert.equal(format("OK"), "OK");
  assert.equal(format([]), "(empty array)");
  assert.equal(format(["a", ["b", "c"]]), '1) "a"\n2) 1) "b"\n   2) "c"');
  assert.equal(format('{"a":[1]}'), '{\n  "a": [\n    1\n  ]\n}');
  assert.equal(format(["k", '{"a":1}']), '1) "k"\n2) {\n     "a": 1\n   }');
  assert.equal(format("[not json"), '"[not json"');
});
