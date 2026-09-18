import vm from "node:vm";
import {
  AbstractCursor,
  BSON,
  ChangeStream,
  Decimal128,
  Long,
  MongoClient,
  ObjectId,
  UUID,
  type Collection,
  type Db,
} from "mongodb";
import { live, plural, serve, table, text, type Output, type Resource, type Session } from "../sdk/index.ts";

const { EJSON } = BSON;

const maxDocs = 200;

export function ref(name: string): string {
  return /^[A-Za-z_$][\w$]*$/.test(name) ? `db.${name}` : `db.getCollection(${JSON.stringify(name)})`;
}

function bind<T extends object>(target: T, extra: Record<string, unknown>, missing?: (p: string) => unknown): T {
  return new Proxy(target, {
    get(t, p) {
      if (typeof p === "string" && p in extra) return extra[p];
      if (typeof p === "string" && !(p in t) && missing) return missing(p);
      const v = Reflect.get(t, p, t);
      return typeof v === "function" ? v.bind(t) : v;
    },
  });
}

export function shell(db: Db): Db {
  const coll = (name: string): Collection => {
    const c = db.collection(name);
    return bind(c, {
      getIndexes: () => c.indexes(),
      count: (filter?: object) => c.countDocuments(filter ?? {}),
    });
  };
  return bind(
    db,
    {
      getCollection: coll,
      getCollectionNames: async () =>
        (await db.listCollections({}, { nameOnly: true }).toArray()).map((c) => c.name),
      runCommand: (cmd: object) => db.command(cmd as never),
      getName: () => db.databaseName,
    },
    coll,
  );
}

function pretty(value: unknown): string {
  return EJSON.stringify(value as never, undefined, 2, { relaxed: true });
}

export async function evaluate(db: Db, q: string, signal: AbortSignal): Promise<Output> {
  const context = vm.createContext({
    db: shell(db),
    ObjectId,
    UUID,
    ISODate: (s?: string) => (s ? new Date(s) : new Date()),
    NumberLong: (v: string | number) => Long.fromString(String(v)),
    NumberDecimal: (v: string | number) => Decimal128.fromString(String(v)),
  });
  const value: unknown = await vm.runInContext(q, context, { timeout: 5000 });

  if (value instanceof ChangeStream) {
    const stream = value;
    return live(signal, "watching changes", async (emit, fail) => {
      stream.on("change", (change) => emit(EJSON.stringify(change, { relaxed: true })));
      stream.on("error", fail);
      return () => stream.close();
    });
  }
  if (value instanceof AbstractCursor) {
    const docs: unknown[] = [];
    for await (const doc of value) {
      if (signal.aborted || docs.length === maxDocs) break;
      docs.push(doc);
    }
    await value.close();
    const more = docs.length === maxDocs ? `, showing first ${maxDocs}` : "";
    return text(docs.map(pretty).join("\n"), plural(docs.length, "document") + more);
  }
  if (value === undefined) return text("", "ok");
  return text(pretty(value), Array.isArray(value) ? plural(value.length, "item") : "");
}

async function connect(uri: string): Promise<Session> {
  const client = new MongoClient(uri, { serverSelectionTimeoutMS: 10_000, appName: "querypro" });
  await client.connect();
  let db = client.db();
  const info = await db.admin().command({ buildInfo: 1 });

  return {
    server: `MongoDB ${info.version}`,
    async resources() {
      const cols = await db.listCollections({}, { nameOnly: true }).toArray();
      return cols
        .map((c) => ({ kind: c.type ?? "collection", name: c.name }))
        .sort((a, b) => a.name.localeCompare(b.name))
        .slice(0, 1000);
    },
    actions(r: Resource) {
      const c = ref(r.name);
      return [
        { name: "Find documents", query: `${c}.find({}).limit(20)` },
        { name: "Count documents", query: `${c}.countDocuments()` },
        { name: "List indexes", query: `${c}.getIndexes()` },
        { name: "Watch changes (live)", query: `${c}.watch()` },
        { name: "Drop collection", query: `${c}.drop()`, danger: true },
      ];
    },
    async query(q, signal) {
      const [cmd, arg] = q.replace(/;$/, "").split(/\s+/);
      if (cmd === "use" && arg) {
        db = client.db(arg);
        return text("", `switched to db ${arg}`);
      }
      if (cmd === "show" && (arg === "dbs" || arg === "databases")) {
        const { databases } = await db.admin().listDatabases();
        return table(["database", "size"], databases.map((d) => [d.name, d.sizeOnDisk ?? 0]));
      }
      if (cmd === "show" && arg === "collections") {
        const cols = await db.listCollections({}, { nameOnly: true }).toArray();
        return table(["collection", "type"], cols.map((c) => [c.name, c.type ?? "collection"]));
      }
      return evaluate(db, q, signal);
    },
    async close() {
      await client.close();
    },
  };
}

if (import.meta.main) serve(connect);
