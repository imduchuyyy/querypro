import { Redis } from "ioredis";
import { live, quote, serve, text, words, type Output, type Resource, type Session } from "../sdk/index.ts";

const maxKeys = 1000;
export function format(v: unknown, indent = ""): string {
  if (v === null || v === undefined) return "(nil)";
  if (typeof v === "number") return `(integer) ${v}`;
  if (v instanceof Error) return `(error) ${v.message}`;
  if (Array.isArray(v)) {
    if (v.length === 0) return "(empty array)";
    return v
      .map((x, i) => {
        const prefix = `${i + 1}) `;
        return (i ? indent : "") + prefix + format(x, indent + " ".repeat(prefix.length));
      })
      .join("\n");
  }
  const s = Buffer.isBuffer(v) ? v.toString() : String(v);
  return s === "OK" ? s : JSON.stringify(s);
}

const reads: Record<string, (k: string) => string> = {
  string: (k) => `GET ${k}`,
  hash: (k) => `HGETALL ${k}`,
  list: (k) => `LRANGE ${k} 0 99`,
  set: (k) => `SSCAN ${k} 0 COUNT 100`,
  zset: (k) => `ZRANGE ${k} 0 99 WITHSCORES`,
  stream: (k) => `XREVRANGE ${k} + - COUNT 50`,
};

function subscribe(client: Redis, cmd: string, channels: string[], signal: AbortSignal): Promise<Output> {
  return live(signal, `subscribed to ${channels.join(", ")}`, async (emit, fail) => {
    const sub = client.duplicate();
    sub.on("error", fail);
    sub.on("message", (ch: string, msg: string) => emit(`${ch} ${msg}`));
    sub.on("pmessage", (_: string, ch: string, msg: string) => emit(`${ch} ${msg}`));
    sub.on("smessage", (ch: string, msg: string) => emit(`${ch} ${msg}`));
    try {
      await sub.connect();
      await sub.call(cmd.toLowerCase(), ...channels);
    } catch (err) {
      sub.disconnect();
      throw err;
    }
    return () => sub.disconnect();
  });
}

function monitor(client: Redis, signal: AbortSignal): Promise<Output> {
  return live(signal, "monitoring commands", async (emit, fail) => {
    const mon = await client.monitor();
    mon.on("error", fail);
    mon.on("monitor", (time: string, argv: string[], source: string, db: string) =>
      emit(`${time} [${db} ${source}] ${argv.map(quote).join(" ")}`),
    );
    return () => mon.disconnect();
  });
}

async function connect(uri: string): Promise<Session> {
  const client = new Redis(uri, {
    lazyConnect: true,
    connectTimeout: 10_000,
    maxRetriesPerRequest: 1,
    connectionName: "querypro",
  });
  let cause: Error | undefined;
  client.on("error", (err) => {
    cause = err;
    console.error("redis:", err.message);
  });
  try {
    await client.connect();
  } catch (err) {
    client.disconnect();
    throw cause ?? err;
  }
  const info = await client.info("server");

  return {
    server: `Redis ${/redis_version:(\S+)/.exec(info)?.[1] ?? "unknown"}`,
    async resources() {
      const keys: string[] = [];
      let cursor = "0";
      do {
        const [next, batch] = await client.scan(cursor, "COUNT", 500);
        cursor = next;
        keys.push(...batch);
      } while (cursor !== "0" && keys.length < maxKeys);
      const names = keys.slice(0, maxKeys).sort();
      const types = (await client.pipeline(names.map((k) => ["type", k])).exec()) ?? [];
      return names.map((name, i) => ({ kind: String(types[i]?.[1] ?? "unknown"), name }));
    },
    actions(r: Resource) {
      const k = quote(r.name);
      const acts = [
        { name: "Time to live", query: `TTL ${k}` },
        { name: "Memory usage", query: `MEMORY USAGE ${k}` },
        { name: "Delete key", query: `DEL ${k}`, danger: true },
      ];
      const read = reads[r.kind];
      return read ? [{ name: "Read value", query: read(k) }, ...acts] : acts;
    },
    async query(q, signal) {
      const [name, ...rest] = words(q);
      if (!name) throw new Error("empty command");
      const cmd = name.toUpperCase();
      if (cmd === "SUBSCRIBE" || cmd === "PSUBSCRIBE" || cmd === "SSUBSCRIBE") {
        if (rest.length === 0) throw new Error(`usage: ${cmd} channel [channel ...]`);
        return subscribe(client, cmd, rest, signal);
      }
      if (cmd === "MONITOR") return monitor(client, signal);
      return text(format(await client.call(cmd, ...rest)));
    },
    async close() {
      client.disconnect();
    },
  };
}

if (import.meta.main) serve(connect);
