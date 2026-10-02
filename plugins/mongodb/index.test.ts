import assert from "node:assert/strict";
import { test } from "node:test";
import { value } from "./index.ts";

test("value turns input into a shell literal", () => {
  assert.equal(value("42"), "42");
  assert.equal(value('{"$gt": 5}'), '{"$gt": 5}');
  assert.equal(value("ada"), '"ada"');
  assert.equal(value('say "hi"'), '"say \\"hi\\""');
  assert.equal(value("64b7f1f0a1b2c3d4e5f60718"), 'ObjectId("64b7f1f0a1b2c3d4e5f60718")');
});
