import { randomUUID } from "node:crypto";
import { Kafka, logLevel, type SASLOptions } from "kafkajs";
import { cell, describe, dispatch, live, plural, quote, serve, table, text, type Commands, type Output, type Session } from "../sdk/index.ts";

const mechanisms = ["plain", "scram-sha-256", "scram-sha-512"];

export function parse(uri: string): { brokers: string[]; ssl: boolean; sasl?: SASLOptions } {
  let rest = uri.trim().replace(/^kafka:\/\//, "");
  const qi = rest.indexOf("?");
  const params = new URLSearchParams(qi < 0 ? "" : rest.slice(qi + 1));
  if (qi >= 0) rest = rest.slice(0, qi);
  rest = rest.replace(/\/$/, "");
  let sasl: SASLOptions | undefined;
  const at = rest.lastIndexOf("@");
  if (at >= 0) {
    const [user, ...pass] = rest.slice(0, at).split(":");
    rest = rest.slice(at + 1);
    const mechanism = params.get("mechanism") ?? "plain";
    if (!mechanisms.includes(mechanism)) throw new Error(`mechanism must be one of ${mechanisms.join(", ")}`);
    sasl = {
      mechanism,
      username: decodeURIComponent(user),
      password: decodeURIComponent(pass.join(":")),
    } as SASLOptions;
  }
  const brokers = rest.split(",").filter(Boolean);
  if (brokers.length === 0) throw new Error("no brokers in URI, e.g. kafka://localhost:9092");
  return { brokers, ssl: params.get("ssl") === "true", sasl };
}

async function connect(uri: string): Promise<Session> {
  const kafka = new Kafka({
    ...parse(uri),
    clientId: "querypro",
    connectionTimeout: 10_000,
    retry: { retries: 2 },
    logLevel: logLevel.ERROR,
  });
  const admin = kafka.admin();
  await admin.connect();
  const cluster = await admin.describeCluster();
  let producer: ReturnType<typeof kafka.producer> | undefined;

  const tail = (topic: string, fromBeginning: boolean, signal: AbortSignal): Promise<Output> =>
    live(signal, `tailing ${topic}`, async (emit, fail) => {
      const groupId = `querypro-${randomUUID()}`;
      const consumer = kafka.consumer({ groupId, allowAutoTopicCreation: false });
      consumer.on(consumer.events.CRASH, (e) => fail(e.payload.error));
      const stop = async () => {
        await consumer.disconnect();
        await admin.deleteGroups([groupId]).catch(() => {});
      };
      try {
        await consumer.connect();
        await consumer.subscribe({ topic, fromBeginning });
        await consumer.run({
          autoCommit: false,
          eachMessage: async ({ partition, message }) => {
            const key = message.key ? cell(message.key.toString()) + " " : "";
            emit(`p${partition}@${message.offset} ${key}${cell(message.value?.toString() ?? "NULL")}`);
          },
        });
      } catch (err) {
        await stop().catch(() => {});
        throw err;
      }
      return stop;
    });

  const commands: Commands = {
    async topics() {
      const { topics } = await admin.fetchTopicMetadata();
      return table(
        ["topic", "partitions"],
        topics.sort((a, b) => a.name.localeCompare(b.name)).map((t) => [t.name, t.partitions.length]),
      );
    },
    async "describe <topic>"([topic]) {
      const [{ topics }, offsets] = await Promise.all([
        admin.fetchTopicMetadata({ topics: [topic] }),
        admin.fetchTopicOffsets(topic),
      ]);
      const byPartition = new Map(offsets.map((o) => [o.partition, o]));
      return table(
        ["partition", "leader", "replicas", "isr", "low", "high"],
        topics[0].partitions
          .sort((a, b) => a.partitionId - b.partitionId)
          .map((p) => {
            const o = byPartition.get(p.partitionId);
            return [p.partitionId, p.leader, p.replicas.join(","), p.isr.join(","), o?.low ?? "", o?.high ?? ""];
          }),
      );
    },
    async groups() {
      const { groups } = await admin.listGroups();
      return table(["group", "protocol"], groups.map((g) => [g.groupId, g.protocolType]));
    },
    "tail <topic> [from-beginning]": ([topic, from], signal) => tail(topic, from === "from-beginning", signal),
    async "produce <topic> <value>"([topic, ...value]) {
      if (!producer) {
        producer = kafka.producer();
        await producer.connect();
      }
      const [meta] = await producer.send({ topic, messages: [{ value: value.join(" ") }] });
      return text("", `produced to ${topic} p${meta.partition}@${meta.baseOffset}`);
    },
    async "create-topic <topic> [partitions]"([topic, partitions = "1"]) {
      await admin.createTopics({ topics: [{ topic, numPartitions: Number(partitions) || 1 }], waitForLeaders: true });
      return text("", `created ${topic}`);
    },
    async "delete-topic <topic>"([topic]) {
      await admin.deleteTopics({ topics: [topic] });
      return text("", `deleted ${topic}`);
    },
  };

  return {
    server: `Kafka ${cluster.clusterId} · ${plural(cluster.brokers.length, "broker")}`,
    async resources() {
      const topics = await admin.listTopics();
      return topics.filter((t) => !t.startsWith("__")).sort().map((name) => ({ kind: "topic", name }));
    },
    actions(r) {
      const t = quote(r.name);
      return [
        { name: "Describe partitions", query: `describe ${t}` },
        { name: "Tail new messages (live)", query: `tail ${t}` },
        { name: "Tail from beginning (live)", query: `tail ${t} from-beginning` },
        { name: "Delete topic", query: `delete-topic ${t}`, danger: true },
      ];
    },
    query: (q, signal) => dispatch(commands, q, signal),
    async close() {
      await Promise.allSettled([admin.disconnect(), producer?.disconnect()]).then((rs) =>
        rs.forEach((r) => r.status === "rejected" && console.error("kafka close:", describe(r.reason))),
      );
    },
  };
}

if (import.meta.main) serve(connect);
