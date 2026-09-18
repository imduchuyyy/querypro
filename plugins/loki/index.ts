import { live, plural, serve, table, text, type Output, type Resource, type Session } from "../sdk/index.ts";

const limit = 500;
const poll = 2000;
const preferred = ["service_name", "app", "job", "container", "namespace"];
const units: Record<string, number> = { s: 1, m: 60, h: 3600, d: 86400 };

interface Entry {
  ts: bigint;
  labels: string;
  line: string;
}

type Data =
  | { resultType: "streams"; result: { stream: Record<string, string>; values: [string, string][] }[] }
  | { resultType: "matrix" | "vector"; result: { metric: Record<string, string>; value?: [number, string]; values?: [number, string][] }[] };

export function labels(set: Record<string, string>): string {
  return "{" + Object.entries(set).map(([k, v]) => `${k}=${JSON.stringify(v)}`).join(", ") + "}";
}

export function since(q: string): { query: string; seconds: number } {
  const m = /^since\s+(\d+)([smhd])\s+/.exec(q);
  return m ? { query: q.slice(m[0].length), seconds: Number(m[1]) * units[m[2]] } : { query: q, seconds: 3600 };
}

export function entries(data: Data): Entry[] {
  if (data.resultType !== "streams") throw new Error("expected a log query, got a metric query");
  return data.result
    .flatMap((s) => s.values.map(([ts, line]) => ({ ts: BigInt(ts), labels: labels(s.stream), line })))
    .sort((a, b) => (a.ts < b.ts ? -1 : a.ts > b.ts ? 1 : 0));
}

function format(e: Entry, withLabels: boolean): string {
  const at = new Date(Number(e.ts / 1_000_000n)).toISOString();
  return withLabels ? `${at} ${e.labels} ${e.line}` : `${at} ${e.line}`;
}

async function connect(uri: string): Promise<Session> {
  const u = new URL(uri);
  const headers: Record<string, string> = {};
  if (u.username) {
    const cred = `${decodeURIComponent(u.username)}:${decodeURIComponent(u.password)}`;
    headers.authorization = "Basic " + Buffer.from(cred).toString("base64");
  }
  const org = u.searchParams.get("org");
  if (org) headers["X-Scope-OrgID"] = org;
  const base = u.origin + u.pathname.replace(/\/$/, "");

  const get = async <T>(path: string, params: Record<string, string> = {}, signal?: AbortSignal): Promise<T> => {
    const timeout = AbortSignal.timeout(30_000);
    const res = await fetch(`${base}/loki/api/v1${path}?${new URLSearchParams(params)}`, {
      headers,
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
    });
    if (!res.ok) throw new Error(`loki ${res.status}: ${(await res.text()).trim().slice(0, 300)}`);
    const body = (await res.json()) as { data?: T; version?: string };
    return (body.data ?? body) as T;
  };

  const range = (query: string, start: bigint, direction: string, signal?: AbortSignal) =>
    get<Data>("/query_range", {
      query,
      start: start.toString(),
      end: (BigInt(Date.now()) * 1_000_000n).toString(),
      limit: String(limit),
      direction,
    }, signal);

  const tail = (query: string, signal: AbortSignal): Promise<Output> =>
    live(signal, `tailing ${query}`, async (emit, fail) => {
      let from = (BigInt(Date.now()) - 10_000n) * 1_000_000n;
      let timer: NodeJS.Timeout | undefined;
      let stopped = false;
      const fetchNew = async () => {
        for (const e of entries(await range(query, from, "forward"))) {
          emit(format(e, false));
          from = e.ts + 1n;
        }
      };
      const tick = () =>
        fetchNew().then(
          () => {
            if (!stopped) timer = setTimeout(tick, poll);
          },
          fail,
        );
      await fetchNew();
      timer = setTimeout(tick, poll);
      return () => {
        stopped = true;
        clearTimeout(timer);
      };
    });

  const run = async (q: string, signal: AbortSignal): Promise<Output> => {
    const { query, seconds } = since(q);
    const data = await range(query, (BigInt(Date.now()) - BigInt(seconds) * 1000n) * 1_000_000n, "backward", signal);
    if (data.resultType === "streams") {
      const es = entries(data);
      const many = new Set(es.map((e) => e.labels)).size > 1;
      const more = es.length === limit ? `, showing latest ${limit}` : "";
      return text(es.map((e) => format(e, many)).join("\n"), plural(es.length, "line") + more);
    }
    return table(
      ["series", "time", "value"],
      data.result.map((s) => {
        const [t, v] = s.value ?? s.values?.at(-1) ?? [0, ""];
        return [labels(s.metric), new Date(t * 1000).toISOString(), v];
      }),
      plural(data.result.length, "series"),
    );
  };

  let server = "Loki";
  try {
    server = `Loki ${(await get<{ version: string }>("/status/buildinfo")).version}`;
  } catch {
    await get<string[]>("/labels");
  }

  return {
    server,
    async resources() {
      const names = (await get<string[]>("/labels")) ?? [];
      const label = preferred.find((p) => names.includes(p)) ?? names.find((n) => !n.startsWith("__"));
      if (!label) return [];
      const values = (await get<string[]>(`/label/${encodeURIComponent(label)}/values`)) ?? [];
      return values.sort().slice(0, 1000).map((v) => ({ kind: label, name: labels({ [label]: v }) }));
    },
    actions(r: Resource) {
      return [
        { name: "Recent logs", query: r.name },
        { name: "Errors only", query: `${r.name} |~ "(?i)error"` },
        { name: "Live tail", query: `tail ${r.name}` },
        { name: "Lines per minute", query: `sum(count_over_time(${r.name}[1m]))` },
      ];
    },
    async query(q, signal) {
      if (q === "labels") return table(["label"], ((await get<string[]>("/labels")) ?? []).map((l) => [l]));
      const tailed = /^tail\s+/.exec(q);
      return tailed ? tail(q.slice(tailed[0].length), signal) : run(q, signal);
    },
    async close() {},
  };
}

if (import.meta.main) serve(connect);
