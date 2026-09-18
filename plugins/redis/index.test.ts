import assert from "node:assert/strict";
import { test } from "node:test";
import { format } from "./index.ts";

test("format mirrors redis-cli", () => {
  assert.equal(format(null), "(nil)");
  assert.equal(format(3), "(integer) 3");
  assert.equal(format("OK"), "OK");
  assert.equal(format([]), "(empty array)");
  assert.equal(format(["a", ["b", "c"]]), '1) "a"\n2) 1) "b"\n   2) "c"');
});
