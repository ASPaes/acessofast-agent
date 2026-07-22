import { createClient } from "jsr:@supabase/supabase-js@2";

const INGEST_SECRET = Deno.env.get("VPS_METRICS_INGEST_SECRET");
const SUPABASE_URL  = Deno.env.get("SUPABASE_URL")!;
const SERVICE_ROLE  = Deno.env.get("SUPABASE_SERVICE_ROLE_KEY")!;

function timingSafeEqual(a: string, b: string): boolean {
  const enc = new TextEncoder();
  const ab = enc.encode(a), bb = enc.encode(b);
  if (ab.length !== bb.length) return false;
  let diff = 0;
  for (let i = 0; i < ab.length; i++) diff |= ab[i] ^ bb[i];
  return diff === 0;
}

Deno.serve(async (req) => {
  if (req.method !== "POST") return new Response("method not allowed", { status: 405 });
  if (!INGEST_SECRET)        return new Response("server misconfigured", { status: 500 });

  const provided = req.headers.get("x-ingest-secret") ?? "";
  if (!timingSafeEqual(provided, INGEST_SECRET)) {
    return new Response("unauthorized", { status: 401 });
  }

  let body: any;
  try { body = await req.json(); }
  catch { return new Response("bad json", { status: 400 }); }

  const num  = (v: unknown) => (typeof v === "number" && isFinite(v) ? v : null);
  const pct  = (v: unknown) => { const n = num(v); return n === null ? null : Math.min(100, Math.max(0, n)); };
  const int  = (v: unknown) => (Number.isInteger(v) ? (v as number) : null);
  const bool = (v: unknown) => (typeof v === "boolean" ? v : null);

  // Mapeia TODOS os campos que o coletor (vps-metrics-agent.sh) envia. A tabela
  // vps_metrics tem as 20 colunas; a v16 desta funcao persistia apenas 8 e
  // descartava 12 no insert (uptime, ncpu, load, iowait, steal, mem/disco
  // absolutos, swap, hbbs_up/hbbr_up) -> ficavam NULL -> "—" no painel.
  const row = {
    host: typeof body.host === "string" && body.host.length <= 64 ? body.host : "relay-1",
    // CPU / carga
    cpu_pct: pct(body.cpu_pct),
    cpu_iowait_pct: num(body.cpu_iowait_pct),
    cpu_steal_pct: num(body.cpu_steal_pct),
    ncpu: int(body.ncpu),
    load1: num(body.load1),
    load5: num(body.load5),
    load15: num(body.load15),
    // Memoria
    mem_pct: pct(body.mem_pct),
    mem_total_mb: int(body.mem_total_mb),
    mem_available_mb: int(body.mem_available_mb),
    swap_used_mb: int(body.swap_used_mb),
    // Disco
    disk_pct: pct(body.disk_pct),
    disk_used_gb: num(body.disk_used_gb),
    disk_total_gb: num(body.disk_total_gb),
    // Rede
    net_rx_bytes: int(body.net_rx_bytes),
    net_tx_bytes: int(body.net_tx_bytes),
    // Sistema / relay
    uptime_seconds: int(body.uptime_seconds),
    hbbs_up: bool(body.hbbs_up),
    hbbr_up: bool(body.hbbr_up),
    active_sessions: int(body.active_sessions),
    relay_mbps: num(body.relay_mbps),
  };

  const supabase = createClient(SUPABASE_URL, SERVICE_ROLE, { auth: { persistSession: false } });
  const { error } = await supabase.from("vps_metrics").insert(row);
  if (error) return new Response(JSON.stringify({ error: error.message }), { status: 500 });
  return new Response(null, { status: 204 });
});
