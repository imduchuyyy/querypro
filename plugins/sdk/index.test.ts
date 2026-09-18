import assert from "node:assert/strict";
import { test } from "node:test";
import { cell, dispatch, live, quote, table, text, words, type Commands } from "./index.ts";

test("words splits quoted arguments", () => {
  assert.deepEqual(words(`SET "a key" 'it''s' "x\\ny\\"z" plain`), ["SET", "a key", "its", 'x\ny"z', "plain"]);
  assert.deepEqual(words(`  GET   ""  `), ["GET", ""]);
  assert.throws(() => words(`GET "open`), /unterminated/);
  for (const s of ["plain", "a key", 'q"uote', "back\\slash", ""]) assert.deepEqual(words(quote(s)), [s]);
});

test("cell and table format values", () => {
  assert.equal(cell(null), "NULL");
  assert.equal(cell(new Date(0)), "1970-01-01T00:00:00.000Z");
  assert.equal(cell(Buffer.from([1, 255])), "\\x01ff");
  assert.equal(cell({ n: 1n }), '{"n":"1"}');
  assert.equal(cell("x".repeat(600)).length, 501);
  const out = table(["n"], Array.from({ length: 1001 }, (_, i) => [i]));
  assert.ok("table" in out && out.table.rows.length === 1000);
  assert.equal(out.summary, "1001 rows, showing first 1000");
  assert.equal(text("x".repeat((1 << 20) + 1)).summary, " (truncated)");
});

test("dispatch checks required arguments", async () => {
  const commands: Commands = {
    "tail <topic> [from]": async (args) => text(args.join(",")),
    topics: async () => text("all"),
  };
  const signal = new AbortController().signal;
  assert.deepEqual(await dispatch(commands, "TOPICS", signal), text("all"));
  assert.deepEqual(await dispatch(commands, "tail t b", signal), text("t,b"));
  await assert.rejects(dispatch(commands, "tail", signal), /usage: tail <topic> \[from\]/);
  await assert.rejects(dispatch(commands, "nope", signal), /unknown command "nope", try: tail/);
});

test("live streams lines and stops on abort", async () => {
  const ac = new AbortController();
  let stopped = 0;
  let push: (line: string) => void = () => {};
  const out = await live(ac.signal, "s", async (emit) => {
    push = emit;
    emit("a");
    return () => stopped++;
  });
  assert.ok("lines" in out);
  const it = out.lines[Symbol.asyncIterator]();
  assert.deepEqual(await it.next(), { value: "a", done: false });
  setTimeout(() => push("b"), 5);
  assert.deepEqual(await it.next(), { value: "b", done: false });
  const pending = it.next();
  ac.abort();
  assert.deepEqual(await pending, { value: undefined, done: true });
  assert.equal(stopped, 1);
});

test("live surfaces failures", async () => {
  const out = await live(new AbortController().signal, "s", async (_, fail) => {
    setTimeout(() => fail(new Error("boom")), 1);
    return () => {};
  });
  assert.ok("lines" in out);
  await assert.rejects(async () => {
    for await (const _ of out.lines);
  }, /boom/);
});
