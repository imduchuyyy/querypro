import pg from "pg";
import { describe, serve, table, text, type Action, type Output, type Resource, type Session } from "../sdk/index.ts";

export function ident(name: string): string {
  return '"' + name.replaceAll('"', '""') + '"';
}

export function split(name: string): [string, string] {
  const dot = name.indexOf(".");
  return dot < 0 ? ["public", name] : [name.slice(0, dot), name.slice(dot + 1)];
}

function qualified(name: string): string {
  const [schema, rel] = split(name);
  return schema === "public" ? ident(rel) : `${ident(schema)}.${ident(rel)}`;
}

const listTables = `SELECT table_schema AS schema, table_name AS name, lower(table_type) AS type
FROM information_schema.tables
WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
ORDER BY 1, 2 LIMIT 1000`;

const meta: Record<string, (arg: string) => [string, unknown[]]> = {
  "\\dt": () => [listTables, []],
  "\\d": (arg) =>
    arg
      ? [
          `SELECT column_name AS column, data_type AS type, is_nullable AS nullable, column_default AS default
FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 ORDER BY ordinal_position`,
          split(arg),
        ]
      : [listTables, []],
  "\\dn": () => [`SELECT nspname AS schema FROM pg_namespace WHERE nspname !~ '^pg_' ORDER BY 1`, []],
  "\\l": () => [`SELECT datname AS database FROM pg_database WHERE NOT datistemplate ORDER BY 1`, []],
};

async function connect(uri: string): Promise<Session> {
  const config = { connectionString: uri, connectionTimeoutMillis: 10_000, application_name: "querypro" };
  const client = new pg.Client(config);
  client.on("error", (err) => console.error("postgres:", describe(err)));
  await client.connect();
  const version = await client.query("SHOW server_version");
  const pid = (client as unknown as { processID: number }).processID;

  const cancel = async () => {
    const c = new pg.Client(config);
    c.on("error", () => {});
    try {
      await c.connect();
      await c.query("SELECT pg_cancel_backend($1)", [pid]);
    } finally {
      await c.end();
    }
  };

  const run = async (q: string, signal: AbortSignal): Promise<Output> => {
    const onAbort = () => cancel().catch((err) => console.error("cancel:", describe(err)));
    signal.addEventListener("abort", onAbort, { once: true });
    try {
      const res = await client.query({ text: q, rowMode: "array" });
      const last = (Array.isArray(res) ? res.at(-1) : res) as pg.QueryArrayResult;
      if (last.fields.length > 0) return table(last.fields.map((f) => f.name), last.rows);
      return text("", [last.command, last.rowCount].filter((v) => v !== null && v !== undefined).join(" "));
    } finally {
      signal.removeEventListener("abort", onAbort);
    }
  };

  return {
    server: `PostgreSQL ${version.rows[0].server_version}`,
    async resources() {
      const { rows } = await client.query<{ schema: string; name: string; type: string }>(listTables);
      return rows.map((r) => ({
        kind: r.type === "base table" ? "table" : r.type,
        name: r.schema === "public" ? r.name : `${r.schema}.${r.name}`,
      }));
    },
    actions(r: Resource) {
      const t = qualified(r.name);
      const acts: Action[] = [
        { name: "Preview rows", query: `SELECT * FROM ${t} LIMIT 50;` },
        { name: "Count rows", query: `SELECT count(*) FROM ${t};` },
        { name: "Describe", query: `\\d ${r.name}` },
      ];
      if (r.kind === "table") acts.push({ name: "Truncate", query: `TRUNCATE ${t};`, danger: true });
      return acts;
    },
    async query(q, signal) {
      if (!q.startsWith("\\")) return run(q, signal);
      const [cmd, ...rest] = q.replace(/;$/, "").split(/\s+/);
      const build = meta[cmd];
      if (!build) throw new Error(`unknown meta-command ${cmd}, try: ${Object.keys(meta).join(" ")}`);
      const [sql, params] = build(rest.join(" "));
      const res = await client.query({ text: sql, values: params, rowMode: "array" });
      return table(res.fields.map((f) => f.name), res.rows);
    },
    async close() {
      await client.end();
    },
  };
}

if (import.meta.main) serve(connect);
