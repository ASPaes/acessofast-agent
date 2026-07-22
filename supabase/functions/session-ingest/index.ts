// AcessoFast — Edge Function: session-ingest (v3)
// Recebe eventos do AGENTE do endpoint: sessao (start/heartbeat/end) em connection_logs
// + presenca ociosa (presence) que so mantem address_book.last_online vivo.
// Deploy com verify_jwt = FALSE (auth propria via token de dispositivo).
// FIX v2: duration_seconds e coluna GERADA -> nunca escrever nela; so setar session_end.
// FIX v3: (1) aceitar event 'presence'; (2) todo sinal do agente atualiza last_online;
//         (3) presence retorna cedo e NAO cria connection_logs (nao e sessao).

import { createClient } from "https://esm.sh/@supabase/supabase-js@2.45.0";

const SUPABASE_URL = Deno.env.get("SUPABASE_URL")!;
const SERVICE_ROLE_KEY = Deno.env.get("SUPABASE_SERVICE_ROLE_KEY")!;

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

async function sha256Hex(input: string): Promise<string> {
  const data = new TextEncoder().encode(input);
  const digest = await crypto.subtle.digest("SHA-256", data);
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

Deno.serve(async (req) => {
  if (req.method !== "POST") return json({ error: "method_not_allowed" }, 405);

  let body: { rustdesk_id?: string; agent_token?: string; event?: string };
  try {
    body = await req.json();
  } catch {
    return json({ error: "invalid_json" }, 400);
  }

  const rustdesk_id = (body.rustdesk_id ?? "").trim();
  const agent_token = body.agent_token ?? "";
  const event = body.event ?? "";

  if (!rustdesk_id || !agent_token || !["start", "heartbeat", "end", "presence"].includes(event)) {
    return json({ error: "missing_or_invalid_fields" }, 400);
  }

  const db = createClient(SUPABASE_URL, SERVICE_ROLE_KEY, {
    auth: { persistSession: false },
  });

  // 1) Resolver o dispositivo pelo rustdesk_id (unico) -> tenant + hash do token.
  const { data: device, error: devErr } = await db
    .from("address_book")
    .select("id, tenant_id, agent_token_hash")
    .eq("rustdesk_id", rustdesk_id)
    .maybeSingle();

  if (devErr) return json({ error: "db_error", detail: devErr.message }, 500);
  if (!device) return json({ error: "device_not_registered" }, 404);
  if (!device.agent_token_hash) return json({ error: "device_not_provisioned" }, 401);

  // 2) Autenticar o agente.
  const presentedHash = await sha256Hex(agent_token);
  if (presentedHash !== device.agent_token_hash) {
    return json({ error: "unauthorized" }, 401);
  }

  const nowIso = new Date().toISOString();

  // 2.1) Qualquer sinal do agente (presenca ociosa OU sessao) prova que a maquina
  //      esta viva agora -> mantem address_book.last_online fresco. E o unico dado
  //      que o painel usa pra pintar 'online' (janela de 120s no frontend).
  const { error: presErr } = await db
    .from("address_book")
    .update({ last_online: nowIso })
    .eq("id", device.id);
  if (presErr) return json({ error: "db_error", detail: presErr.message }, 500);

  // 2.2) Presenca ociosa: nao e sessao. Ja atualizamos last_online -> retorna cedo.
  //      NAO cria/atualiza connection_logs (senao inventaria sessao fantasma).
  if (event === "presence") {
    return json({ ok: true, action: "presence" });
  }

  async function latestActive() {
    const { data } = await db
      .from("connection_logs")
      .select("id, session_start")
      .eq("rustdesk_id", rustdesk_id)
      .eq("status", "active")
      .order("session_start", { ascending: false })
      .limit(1)
      .maybeSingle();
    return data;
  }

  // 3) Tratar o evento de sessao.
  if (event === "start" || event === "heartbeat") {
    const active = await latestActive();

    if (active) {
      const { error } = await db
        .from("connection_logs")
        .update({ last_heartbeat_at: nowIso })
        .eq("id", active.id);
      if (error) return json({ error: "db_error", detail: error.message }, 500);
      return json({ ok: true, session_id: active.id, action: "heartbeat" });
    }

    const { data: inserted, error } = await db
      .from("connection_logs")
      .insert({
        tenant_id: device.tenant_id,
        rustdesk_id,
        address_book_id: device.id,
        status: "active",
        session_start: nowIso,
        last_heartbeat_at: nowIso,
        notes: "Acesso externo (nao iniciado pelo painel)",
      })
      .select("id")
      .single();
    if (error) return json({ error: "db_error", detail: error.message }, 500);
    return json({ ok: true, session_id: inserted.id, action: "created_external" });
  }

  // event === "end": fecha a sessao. NAO escreve duration_seconds (coluna gerada).
  const active = await latestActive();
  if (!active) return json({ ok: true, action: "noop_no_active_session" });

  const { error } = await db
    .from("connection_logs")
    .update({
      session_end: nowIso,
      status: "ended",
      last_heartbeat_at: nowIso,
    })
    .eq("id", active.id);
  if (error) return json({ error: "db_error", detail: error.message }, 500);

  const durationSec = Math.max(
    0,
    Math.round((Date.now() - new Date(active.session_start).getTime()) / 1000),
  );
  return json({ ok: true, session_id: active.id, action: "ended", duration_seconds: durationSec });
});
