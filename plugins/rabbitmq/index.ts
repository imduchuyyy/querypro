import amqp, { type Channel, type ChannelModel, type ConsumeMessage, type GetMessage } from "amqplib";
import { cell, describe, dispatch, live, quote, serve, table, text, type Commands, type Session } from "../sdk/index.ts";

export function endpoints(uri: string) {
  const u = new URL(uri);
  const api = (u.searchParams.get("management") ?? `http://${u.hostname}:15672`).replace(/\/$/, "");
  u.searchParams.delete("management");
  const user = decodeURIComponent(u.username || "guest");
  const pass = decodeURIComponent(u.password || "guest");
  return {
    amqp: u.toString(),
    api,
    vhost: decodeURIComponent(u.pathname.slice(1)) || "/",
    auth: "Basic " + Buffer.from(`${user}:${pass}`).toString("base64"),
  };
}

function line(msg: ConsumeMessage): string {
  const { exchange, routingKey } = msg.fields;
  return `${exchange || "(default)"} ${routingKey} ${cell(msg.content.toString())}`;
}

async function connect(uri: string): Promise<Session> {
  const ep = endpoints(uri);
  const conn: ChannelModel = await amqp.connect(ep.amqp, { timeout: 10_000, clientProperties: { connection_name: "querypro" } });
  conn.on("error", (err) => console.error("rabbitmq:", describe(err)));
  const props = (conn.connection as unknown as { serverProperties?: Record<string, unknown> }).serverProperties ?? {};
  const vhost = encodeURIComponent(ep.vhost);

  const api = async <T>(path: string): Promise<T> => {
    const res = await fetch(ep.api + path, {
      headers: { authorization: ep.auth },
      signal: AbortSignal.timeout(10_000),
    });
    if (!res.ok) throw new Error(`management API ${ep.api}${path}: ${res.status} ${res.statusText}`);
    return (await res.json()) as T;
  };

  const withChannel = async <T>(fn: (ch: Channel) => Promise<T>): Promise<T> => {
    const ch = await conn.createChannel();
    ch.on("error", () => {});
    try {
      return await fn(ch);
    } finally {
      await ch.close().catch(() => {});
    }
  };

  const stream = (signal: AbortSignal, summary: string, setup: (ch: Channel) => Promise<string>, ack: boolean) =>
    live(signal, summary, async (emit, fail) => {
      const ch = await conn.createChannel();
      ch.on("error", fail);
      ch.on("close", () => fail(new Error("channel closed")));
      try {
        const queue = await setup(ch);
        await ch.consume(queue, (msg) => {
          if (!msg) return fail(new Error("consumer cancelled by broker"));
          emit(line(msg));
          if (ack) ch.ack(msg);
        }, { noAck: !ack });
      } catch (err) {
        await ch.close().catch(() => {});
        throw err;
      }
      return () => ch.close().catch(() => {});
    });

  const commands: Commands = {
    async queues() {
      const qs = await api<Record<string, unknown>[]>(`/api/queues/${vhost}`);
      return table(
        ["name", "messages", "ready", "unacked", "consumers", "state"],
        qs.map((q) => [q.name, q.messages ?? 0, q.messages_ready ?? 0, q.messages_unacknowledged ?? 0, q.consumers ?? 0, q.state ?? ""]),
      );
    },
    async exchanges() {
      const xs = await api<Record<string, unknown>[]>(`/api/exchanges/${vhost}`);
      return table(["name", "type", "durable"], xs.map((x) => [x.name || "(default)", x.type, x.durable]));
    },
    async "bindings <exchange>"([exchange]) {
      const bs = await api<Record<string, unknown>[]>(`/api/exchanges/${vhost}/${encodeURIComponent(exchange)}/bindings/source`);
      return table(["destination", "type", "routing key"], bs.map((b) => [b.destination, b.destination_type, b.routing_key]));
    },
    "peek <queue> [n]": ([queue, n = "5"]) =>
      withChannel(async (ch) => {
        const msgs: GetMessage[] = [];
        for (let i = 0; i < Math.min(Number(n) || 5, 100); i++) {
          const msg = await ch.get(queue, { noAck: false });
          if (!msg) break;
          msgs.push(msg);
        }
        if (msgs.length > 0) ch.nackAll(true);
        return table(
          ["exchange", "routing key", "redelivered", "body"],
          msgs.map((m) => [m.fields.exchange || "(default)", m.fields.routingKey, m.fields.redelivered, m.content.toString()]),
        );
      }),
    "tap <exchange> [pattern]": ([exchange, pattern = "#"], signal) =>
      stream(signal, `tapping ${exchange} ${pattern}`, async (ch) => {
        const { queue } = await ch.assertQueue("", { exclusive: true, autoDelete: true });
        await ch.bindQueue(queue, exchange, pattern);
        return queue;
      }, false),
    "consume <queue>": ([queue], signal) =>
      stream(signal, `consuming ${queue}`, async (ch) => {
        await ch.checkQueue(queue);
        return queue;
      }, true),
    "publish <exchange> <routing-key> <body>": ([exchange, key, ...body]) =>
      withChannel(async (ch) => {
        ch.publish(exchange, key, Buffer.from(body.join(" ")));
        return text("", `published to ${exchange || "(default)"} ${key}`);
      }),
    "declare <queue>": ([queue]) =>
      withChannel(async (ch) => {
        const { messageCount } = await ch.assertQueue(queue, { durable: true });
        return text("", `declared ${queue} (${messageCount} messages)`);
      }),
    "purge <queue>": ([queue]) =>
      withChannel(async (ch) => {
        const { messageCount } = await ch.purgeQueue(queue);
        return text("", `purged ${messageCount} messages from ${queue}`);
      }),
  };

  return {
    server: `RabbitMQ ${props.version ?? "unknown"}`,
    async resources() {
      const [qs, xs] = await Promise.all([
        api<{ name: string }[]>(`/api/queues/${vhost}?columns=name`),
        api<{ name: string }[]>(`/api/exchanges/${vhost}?columns=name`),
      ]);
      return [
        ...qs.map((q) => ({ kind: "queue", name: q.name })),
        ...xs.filter((x) => x.name && !x.name.startsWith("amq.")).map((x) => ({ kind: "exchange", name: x.name })),
      ];
    },
    actions(r) {
      const n = quote(r.name);
      if (r.kind === "exchange") {
        return [
          { name: "Show bindings", query: `bindings ${n}` },
          { name: "Tap messages (live)", query: `tap ${n} #` },
        ];
      }
      return [
        { name: "Peek messages", query: `peek ${n} 5` },
        { name: "Consume and ack (live)", query: `consume ${n}`, danger: true },
        { name: "Purge queue", query: `purge ${n}`, danger: true },
      ];
    },
    query: (q, signal) => dispatch(commands, q, signal),
    async close() {
      await conn.close();
    },
  };
}

if (import.meta.main) serve(connect);
