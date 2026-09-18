import { randomUUID } from "node:crypto";
import { once } from "node:events";
import { fileURLToPath } from "node:url";
import * as grpc from "@grpc/grpc-js";
import * as loader from "@grpc/proto-loader";

export interface Resource {
  kind: string;
  name: string;
}

export interface Action {
  name: string;
  query: string;
  danger?: boolean;
}

export type Output =
  | { table: { columns: string[]; rows: string[][] }; summary: string }
  | { text: string; summary: string }
  | { lines: AsyncIterable<string>; summary: string };

export interface Session {
  server: string;
  resources(): Promise<Resource[]>;
  actions(resource: Resource): Action[];
  query(query: string, signal: AbortSignal): Promise<Output>;
  close(): Promise<void>;
}

export const handshake = "querypro-plugin 1";
export const maxRows = 1000;
const maxCell = 500;
const maxText = 1 << 20;
const maxQueue = 1000;

export class Status extends Error {
  code: grpc.status;
  constructor(code: grpc.status, message: string) {
    super(message);
    this.code = code;
  }
}

export function describe(err: unknown): string {
  if (err instanceof AggregateError && err.errors.length > 0) {
    return err.errors.map(describe).join("; ");
  }
  if (err instanceof Error) {
    const code = (err as { code?: unknown }).code;
    return err.message || String(code ?? err.name);
  }
  return String(err);
}

const escapes: Record<string, string> = { n: "\n", r: "\r", t: "\t" };

export function words(line: string): string[] {
  const out: string[] = [];
  let cur = "";
  let quote = "";
  let has = false;
  for (let i = 0; i < line.length; i++) {
    const ch = line[i];
    if (quote) {
      if (ch === "\\" && quote === '"' && i + 1 < line.length) {
        const next = line[++i];
        cur += escapes[next] ?? next;
      } else if (ch === quote) quote = "";
      else cur += ch;
    } else if (ch === '"' || ch === "'") {
      quote = ch;
      has = true;
    } else if (/\s/.test(ch)) {
      if (has) out.push(cur);
      cur = "";
      has = false;
    } else {
      cur += ch;
      has = true;
    }
  }
  if (quote) throw new Error("unterminated quote");
  if (has) out.push(cur);
  return out;
}

export function quote(arg: string): string {
  return /^[^\s"'\\]+$/.test(arg) ? arg : JSON.stringify(arg);
}

export function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}

export function json(value: unknown, indent?: number): string {
  return JSON.stringify(value, (_, v) => (typeof v === "bigint" ? v.toString() : v), indent) ?? "";
}

export function cell(value: unknown): string {
  let s: string;
  if (value === null || value === undefined) s = "NULL";
  else if (typeof value === "string") s = value;
  else if (value instanceof Date) s = value.toISOString();
  else if (value instanceof Uint8Array) s = "\\x" + Buffer.from(value).toString("hex");
  else if (typeof value === "object") s = json(value);
  else s = String(value);
  return s.length > maxCell ? s.slice(0, maxCell) + "…" : s;
}

export function table(columns: string[], rows: unknown[][], summary?: string): Output {
  const more = rows.length > maxRows ? `, showing first ${maxRows}` : "";
  return {
    table: { columns, rows: rows.slice(0, maxRows).map((r) => r.map(cell)) },
    summary: (summary ?? plural(rows.length, "row")) + more,
  };
}

export function text(value: string, summary = ""): Output {
  if (value.length > maxText) {
    return { text: value.slice(0, maxText) + "\n… truncated", summary: summary + " (truncated)" };
  }
  return { text: value, summary };
}

export async function live(
  signal: AbortSignal,
  summary: string,
  start: (emit: (line: string) => void, fail: (err: unknown) => void) => Promise<() => unknown>,
): Promise<Output> {
  const queue: string[] = [];
  let failure: unknown;
  let stopped = false;
  let wake: (() => void) | undefined;
  const notify = () => {
    wake?.();
    wake = undefined;
  };
  const stop = await start(
    (line) => {
      if (queue.push(line) > maxQueue) queue.shift();
      notify();
    },
    (err) => {
      failure ??= err ?? new Error("stream failed");
      notify();
    },
  );
  const close = async () => {
    if (stopped) return;
    stopped = true;
    notify();
    try {
      await stop();
    } catch (err) {
      console.error("stopping stream:", err);
    }
  };
  signal.addEventListener("abort", () => void close(), { once: true });
  if (signal.aborted) await close();
  async function* lines() {
    try {
      for (;;) {
        const line = queue.shift();
        if (line !== undefined) {
          yield line;
          continue;
        }
        if (stopped) return;
        if (failure) throw failure;
        await new Promise<void>((resolve) => (wake = resolve));
      }
    } finally {
      await close();
    }
  }
  return { lines: lines(), summary };
}

export type Commands = Record<string, (args: string[], signal: AbortSignal) => Promise<Output>>;

export async function dispatch(commands: Commands, q: string, signal: AbortSignal): Promise<Output> {
  const [name = "", ...args] = words(q);
  const spec = Object.keys(commands).find((k) => k.split(" ")[0] === name.toLowerCase());
  if (!spec) {
    throw new Error(`unknown command ${JSON.stringify(name)}, try: ${Object.keys(commands).join(", ")}`);
  }
  if (args.length < (spec.match(/<[^>]+>/g) ?? []).length) throw new Error(`usage: ${spec}`);
  return commands[spec](args, signal);
}

type Call<T> = grpc.ServerUnaryCall<T, unknown>;

function status(err: unknown): Partial<grpc.StatusObject> {
  const code = err instanceof Status ? err.code : grpc.status.UNKNOWN;
  return { code, details: describe(err) };
}

function unary<T>(fn: (req: T) => Promise<unknown>): grpc.handleUnaryCall<T, unknown> {
  return (call: Call<T>, done) => {
    fn(call.request).then(
      (res) => done(null, res),
      (err) => done(status(err)),
    );
  };
}

async function stream(call: grpc.ServerWritableStream<{ query: string }, unknown>, out: Output, signal: AbortSignal) {
  if ("table" in out) {
    const { columns, rows } = out.table;
    call.write({ table: { columns, rows: rows.map((cells) => ({ cells })) }, summary: out.summary });
  } else if ("text" in out) {
    call.write({ text: out.text, summary: out.summary });
  } else {
    call.write({ live: {}, summary: out.summary });
    for await (const line of out.lines) {
      if (!call.write({ line })) await once(call, "drain", { signal });
    }
  }
  call.end();
}

export function serve(connect: (uri: string) => Promise<Session>): void {
  const socket = process.env.QUERYPRO_SOCKET;
  if (!socket) {
    console.error("QUERYPRO_SOCKET is not set: plugins are started by querypro");
    process.exit(2);
  }
  console.log = console.info = console.debug = console.error;
  process.on("unhandledRejection", (err) => console.error("unhandled rejection:", err));

  const proto = fileURLToPath(new URL("../../proto/querypro/plugin/v1/plugin.proto", import.meta.url));
  const def = loader.loadSync(proto, { longs: String, enums: String, defaults: true, oneofs: true });
  const pkg = grpc.loadPackageDefinition(def) as unknown as {
    querypro: { plugin: { v1: { PluginService: grpc.ServiceClientConstructor } } };
  };

  const sessions = new Map<string, Session>();
  const get = (id: string) => {
    const s = sessions.get(id);
    if (!s) throw new Status(grpc.status.NOT_FOUND, "session not found, reconnect");
    return s;
  };

  const server = new grpc.Server();
  server.addService(pkg.querypro.plugin.v1.PluginService.service, {
    Connect: unary(async ({ uri }: { uri: string }) => {
      const s = await connect(uri);
      const id = randomUUID();
      sessions.set(id, s);
      return { session: id, server: s.server };
    }),
    Disconnect: unary(async ({ session }: { session: string }) => {
      const s = sessions.get(session);
      sessions.delete(session);
      await s?.close();
      return {};
    }),
    Resources: unary(async ({ session }: { session: string }) => ({
      resources: await get(session).resources(),
    })),
    Actions: unary(async ({ session, resource }: { session: string; resource: Resource }) => ({
      actions: get(session).actions(resource),
    })),
    Query: (call: grpc.ServerWritableStream<{ session: string; query: string }, unknown>) => {
      const ac = new AbortController();
      call.on("cancelled", () => ac.abort());
      (async () => stream(call, await get(call.request.session).query(call.request.query, ac.signal), ac.signal))()
        .catch((err) => {
          if (!ac.signal.aborted) call.emit("error", status(err));
        })
        .finally(() => ac.abort());
    },
  });

  let closing = false;
  const shutdown = () => {
    if (closing) return;
    closing = true;
    setTimeout(() => process.exit(0), 3000).unref();
    server.forceShutdown();
    Promise.allSettled([...sessions.values()].map((s) => s.close())).then(() => process.exit(0));
  };
  process.stdin.on("end", shutdown).on("close", shutdown).resume();
  process.on("SIGTERM", shutdown).on("SIGINT", shutdown);

  server.bindAsync(`unix://${socket}`, grpc.ServerCredentials.createInsecure(), (err) => {
    if (err) {
      console.error("listen:", err);
      process.exit(1);
    }
    process.stdout.write(handshake + "\n");
  });
}
