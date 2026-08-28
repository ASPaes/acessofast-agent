// AcessoFast — Agente de Sessao + Matriculador (binario unico).
//
// Sem argumentos          -> roda como servico do SO (SCM no Windows, launchd no macOS).
// Com --enroll            -> executa a matricula do endpoint UMA vez e sai.
//
// Deteccao de sessao: o agente faz tail do log do cliente branded (namespace
// AcessoFast, subpasta server) e pareia as linhas "#N Connection opened" /
// "#N Connection closed" que o motor RustDesk emite por conexao. Mantem o
// conjunto de #N abertos: primeira abertura -> "start"; ultimo fechamento ->
// "end"; heartbeat enquanto houver #N aberto; "presence" quando ocioso. Suporta
// sessoes simultaneas (varios #N) e expira #N preso ha >24h (close perdido numa
// rotacao de log).
//
// NOTA (aprendido em log real, nao deduzido): o marcador "connection count: N"
// NAO serve — ele reloga varias vezes durante a sessao e NUNCA cai para 0 no
// fim (o encoder e destruido sem relogar a contagem). O par #N opened/closed
// (src/server/connection.rs) e o unico sinal confiavel de inicio/fim.
//
// Loga em <baseDir>/agent.log — o baseDir e definido por plataforma
// (plat_windows.go / plat_darwin.go).
//
// O caminho --enroll vive em enroll.go: le o RustDesk ID, chama enroll-device,
// grava agent.token + rustdesk_id com ACL restrita.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Constantes e estado compartilhados (FONTE UNICA — enroll.go usa estes)
// ---------------------------------------------------------------------------

const (
	serviceName       = "AcessoFastAgent"
	ingestURL         = "https://plmfyibyrowbgjjyblcl.supabase.co/functions/v1/session-ingest"
	pollInterval      = 3 * time.Second
	heartbeatInterval = 20 * time.Second
	presenceInterval  = 60 * time.Second

	// Fase 3 §3.1 — janela de carencia (debounce) do fim de sessao. Ao esvaziar o
	// conjunto de #N, NAO encerra/rotaciona na hora: espera este prazo por uma
	// reconexao (blip/queda curta, validado no teste #3). Reconexao dentro do prazo =
	// mesma sessao (sem end, sem rotacao, sem churn de quota). Prazo vazio = fim real.
	graceWindow = 60 * time.Second

	// duracaoMinAutenticada — limiar de "esta conexao chegou a virar sessao".
	//
	// O cliente loga "#N Connection opened" no ACCEPT do TCP, ANTES de validar a senha:
	// uma tentativa RECUSADA produz opened + closed identicos aos de uma sessao real.
	// Rotacionar em cima disso era um laco que se alimentava sozinho — a senha que o
	// painel acabara de entregar morria por causa da propria tentativa que falhou, e a
	// seguinte tambem (caso 007BOMBONIERE03, 25/08/2026: sessoes de 69-81s em serie,
	// = ~10s de conexao recusada + graceWindow).
	//
	// Sinal primario e o peer_id (so sai depois do login aceito); o limiar de tempo e a
	// rede de protecao para o cliente que nao emite peer_id — uma senha recusada fecha
	// em segundos, uma sessao de verdade passa disto.
	duracaoMinAutenticada = 15 * time.Second
)

// Caminhos derivados do baseDir, que e definido POR PLATAFORMA (plat_windows.go /
// plat_darwin.go). Sao var e nao const porque filepath.Join resolve o separador em
// tempo de execucao — no Windows o resultado e exatamente o de sempre
// (C:\ProgramData\AcessoFast\agent.token e irmaos), byte por byte.
var (
	tokenFile = filepath.Join(baseDir, "agent.token")
	ridFile   = filepath.Join(baseDir, "rustdesk_id")
	logFile   = filepath.Join(baseDir, "agent.log")
)

// anonKey e publica por design (role=anon). Preferencia: injetar no CI via
//
//	go build -ldflags "-X main.anonKey=<ANON_KEY>"
//
// O fallback hardcoded preserva o comportamento atual do agente caso o build
// nao injete (o binario que ja roda em producao tem a chave embutida).
var anonKey = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6InBsbWZ5aWJ5cm93YmdqanlibGNsIiwicm9sZSI6ImFub24iLCJpYXQiOjE3ODM2NDMyNjIsImV4cCI6MjA5OTIxOTI2Mn0.grcQYqN3fHvFTWI0AFPWG66k1wONuGqZ5yMt07qcjxE"

// version identifica o build deste binario. Injetado pelo CI como
//
//	go build -ldflags "-X main.version=2026.08.07-a1b2c3d"
//
// Formato AAAA.MM.DD-<sha7>: a data vem primeiro e em largura fixa de proposito —
// assim o painel ordena builds comparando string, sem precisar de semver. Build
// local fica "dev", que o painel trata como versao desconhecida.
//
// Vai em todo POST a session-ingest (agent_version). E o que da visibilidade de
// qual maquina roda qual build; sem isso a frota so e auditavel abrindo maquina
// por maquina.
var version = "dev"

var (
	// Marcadores reais confirmados em log de producao (connection.rs):
	//   #619 Connection opened from 189.4.111.147:12288.
	//   #619 Connection closed: Peer close
	openedRe = regexp.MustCompile(`#(\d+) Connection opened`)
	closedRe = regexp.MustCompile(`#(\d+) Connection closed`)
	// AcessoFast (auto-adocao): linha injetada no cliente logando o rustdesk_id do peer
	// (controlador) apos o login: `#<N> peer_id <id>`. Ver o patch no build-client.yml.
	peerIdRe = regexp.MustCompile(`#(\d+) peer_id (\d+)`)
	diagRe   = regexp.MustCompile(`(?i)Connection opened|Connection closed|new client|LoginRequest|authorized|peer_id`)

	logMu sync.Mutex
	logFH *os.File

	rustdeskID string
	token      string
)

func logln(format string, a ...interface{}) {
	logMu.Lock()
	defer logMu.Unlock()
	line := fmt.Sprintf("%s  %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
	if logFH != nil {
		logFH.WriteString(line)
		logFH.Sync()
	}
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Descobre o rustdesk_id: arquivo dedicado (autoridade do instalador) ou, fallback,
// varre a config do cliente branded procurando o campo id. Namespace = AcessoFast
// (confirmado em maquina real: ...\AcessoFast\config\AcessoFast2.toml).
func discoverRustdeskID() string {
	if v := readTrim(ridFile); v != "" {
		return v
	}
	globs := clientConfigGlobs()
	idRe := regexp.MustCompile(`(?m)^\s*id\s*=\s*['"]?([0-9]{6,})`)
	for _, g := range globs {
		matches, _ := filepath.Glob(g)
		for _, m := range matches {
			b, err := os.ReadFile(m)
			if err != nil {
				continue
			}
			if mm := idRe.FindStringSubmatch(string(b)); mm != nil {
				return mm[1]
			}
		}
	}
	return ""
}

// findRustdeskLog aponta para o log de SESSAO do cliente branded. Namespace segue
// o app-name (AcessoFast_rCURRENT.log), e SO interessa o log da subpasta "server":
// e a unica onde o motor escreve "#N Connection opened/closed" (connection.rs,
// lado servidor = quem RECEBE a sessao). Logs fora de server\ (UI / lado que
// controla) NAO tem esses eventos.
//
// Aprendido em teste real (maquina do Ryan): escolher pelo mtime pegava o log de
// usuario (C:\Users\...\log\AcessoFast_rCURRENT.log), mais recente porem SEM os
// eventos -> agente cego. Por isso agora so olhamos server\; se nao existir,
// retorna "" e o poll re-tenta a cada 3s (o log server\ nasce na 1a sessao
// recebida — ate la nao ha nada pra detectar naquela maquina).
func findRustdeskLog() string {
	serverLogs := clientServerLogGlobs()
	var best string
	var bestT time.Time
	for _, p := range serverLogs {
		matches, _ := filepath.Glob(p)
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil && fi.ModTime().After(bestT) {
				bestT = fi.ModTime()
				best = m
			}
		}
	}
	return best
}

var httpClient = &http.Client{Timeout: 12 * time.Second}

// ingestResp: campos da resposta da session-ingest que o agente usa. Billing B2:
// hard_cap_at = instante do corte rigido de 2h do free (RFC3339). Vem so em
// start/heartbeat de sessao FREE iniciada pelo painel; ausente/vazio nos demais.
type ingestResp struct {
	HardCapAt string `json:"hard_cap_at"`
	// Passo 2: bloco de auto-update. So vem em 'presence' (maquina ociosa) e so
	// quando o alvo resolvido no servidor difere da versao que este agente reportou.
	// Ver update.go.
	Update *updateInfo `json:"update"`
}

// postEvent posta um evento de sessao e devolve o hard_cap_at da resposta (zero se
// ausente/ilegivel/erro). So start/heartbeat carregam cap; end/presence retornam zero.
//
// Wrapper de postEventFull: a esmagadora maioria dos chamadores so quer o cap, e
// nenhum evento alem de 'presence' pode trazer update.
func postEvent(event string, controllerID string) time.Time {
	cap, _ := postEventFull(event, controllerID)
	return cap
}

// postEventFull e o POST de verdade. Devolve tambem o bloco de update, que so o
// ramo de 'presence' do worker consome.
func postEventFull(event string, controllerID string) (time.Time, *updateInfo) {
	// Guarda: sem credencial, nao adianta postar — a session-ingest rejeitaria.
	// Evita ruido de POST invalido a cada 60s quando a matricula ainda nao rodou.
	if token == "" || rustdeskID == "" {
		logln("SKIP %s: token/rustdesk_id ausente (matricula pendente?)", event)
		return time.Time{}, nil
	}
	m := map[string]string{
		"rustdesk_id": rustdeskID, "agent_token": token, "event": event,
		// Visibilidade de frota: pega carona no sinal que ja existe (presence 60s /
		// heartbeat 20s), entao o painel sabe a versao de uma maquina ligada em ate
		// 1 min, sem nenhuma requisicao nova. O servidor grava em
		// address_book.agent_version.
		"agent_version": version,
	}
	// controller_rustdesk_id (auto-adocao): rustdesk_id do peer (controlador), quando
	// conhecido. So no 'start' serve de gatilho pro servidor auto-adotar um device ainda
	// nao registrado no tenant do controlador; nos demais eventos e ignorado.
	if controllerID != "" {
		m["controller_rustdesk_id"] = controllerID
	}
	payload, _ := json.Marshal(m)
	req, err := http.NewRequest("POST", ingestURL, bytes.NewReader(payload))
	if err != nil {
		logln("POST %s erro ao montar req: %v", event, err)
		return time.Time{}, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", anonKey)
	req.Header.Set("Authorization", "Bearer "+anonKey)
	resp, err := httpClient.Do(req)
	if err != nil {
		logln("POST %s FALHOU: %v", event, err)
		return time.Time{}, nil
	}
	defer resp.Body.Close()

	// O teto era 400 bytes, o que bastava enquanto a resposta so trazia hard_cap_at.
	// O bloco de update (Passo 2) nao cabe nisso — so a assinatura Ed25519 em base64
	// ja sao 88 caracteres, mais url e sha256 — e o corte no meio faria o
	// json.Unmarshal falhar de um jeito mudo: o agente simplesmente nunca atualizaria.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))

	// O log segue truncado em 400: quem le agent.log quer ver o ok/erro, e despejar
	// a assinatura inteira a cada 60s so inchava o arquivo na maquina do cliente.
	logged := strings.TrimSpace(string(body))
	if len(logged) > 400 {
		logged = logged[:400] + "…"
	}
	logln("POST %s -> HTTP %d  %s", event, resp.StatusCode, logged)

	var r ingestResp
	if json.Unmarshal(body, &r) != nil {
		return time.Time{}, nil
	}

	// Billing B2: extrai o hard_cap_at (se veio). Tolera fracao de segundo do timestamptz.
	cap := time.Time{}
	if r.HardCapAt != "" {
		if ts, e := time.Parse(time.RFC3339, r.HardCapAt); e == nil {
			cap = ts
		} else if ts, e := time.Parse(time.RFC3339Nano, r.HardCapAt); e == nil {
			cap = ts
		} else {
			logln("WARN hard_cap_at ilegivel: %q", r.HardCapAt)
		}
	}
	return cap, r.Update
}

// tailer acompanha o log e mantem o conjunto de conexoes (#N) abertas.
// active == len(open) > 0. Estado sequencial: so e tocado dentro do select do
// worker (uma unica goroutine) — sem necessidade de mutex.
type tailer struct {
	path   string
	offset int64
	primed bool
	open   map[string]time.Time // #N aberto -> instante em que vimos o "opened"

	// pending: sobra da ultima leitura SEM "\n" no fim. O poll le o arquivo a cada 3s e
	// pode cair no meio de uma linha que o cliente ainda esta escrevendo; sem guardar o
	// pedaco, um "#N Connection closed" partido virava dois fragmentos, nenhum casava
	// com o regex e a sessao ficava presa (fantasma). Ver fatiaLinhas().
	pending string

	// graceUntil (§3.1): quando o conjunto de #N esvazia, arma-se o prazo de carencia.
	// Zero = desarmado. Uma reconexao antes do prazo cancela; o prazo estourando vazio
	// dispara o fim de sessao adiado (postEvent("end") + rotateNow) via checkGrace().
	graceUntil time.Time

	// hardCapUntil (Billing B2): corte rigido de 2h do free. Armado quando a resposta
	// da session-ingest (start/heartbeat) traz hard_cap_at. Zero = sem cap (credito/
	// plano/externo). checkHardCap() corta ao vencer: rotaciona + reinicia o cliente
	// (derruba a sessao). Limpo no fim real da sessao pra nao vazar pro proximo atendimento.
	hardCapUntil time.Time

	// clientStart: horario de inicio do processo do servico do cliente branded. Muda
	// quando o cliente REINICIA (reboot/crash/restart manual/corte) — sinal de que TODAS
	// as conexoes cairam. O restart derruba a sessao SEM emitir "#N closed" no log, entao
	// sem isto o #N ficava preso em open e virava FANTASMA. Ver detectClientRestart().
	clientStart time.Time

	// controllerID (auto-adocao): rustdesk_id do PEER (controlador = maquina do tecnico)
	// da sessao atual, parseado da linha `#<N> peer_id <id>` que o cliente loga apos o
	// login. Enviado no 'start' -> o servidor auto-adota um device ainda nao registrado
	// no tenant do controlador. Vem DEPOIS do "opened" (o login e posterior ao accept TCP),
	// por isso o start inicial vai sem ele e re-postamos start ao aprender o peer_id.
	// Limpo no fim real da sessao (nao vaza pro proximo atendimento).
	controllerID string

	// semSocketDesde: instante em que o cliente branded passou a NAO ter nenhum socket
	// de sessao, com #N ainda aberto. Zero = ha socket (ou nao houve leitura confiavel).
	// Segunda fonte fora do log, como clientStart: se o "closed" se perdeu de vez, o
	// socket e quem prova que a sessao acabou. Ver checkSessaoViva() em sessao_socket.go.
	semSocketDesde time.Time

	// clienteParadoDesde / ultimaSubidaCliente: estado da autocura do servico do
	// cliente (vigia_cliente.go). Zero = servico no ar / nunca tentamos subir.
	clienteParadoDesde  time.Time
	ultimaSubidaCliente time.Time

	// autenticada: alguma conexao DESTA sessao passou do login. Falso enquanto so
	// houve accept de TCP — que e o que uma senha recusada produz. So a sessao
	// autenticada gira a senha efemera no fim (ver rotacionarSeAutenticada). Vale pela
	// sessao inteira, inclusive atravessando a carencia: e limpo so no fim REAL.
	autenticada bool
}

// marcaAutenticada registra que a sessao passou do login, uma vez so por sessao.
func (t *tailer) marcaAutenticada(motivo string) {
	if t.autenticada {
		return
	}
	t.autenticada = true
	logln(">>> sessao autenticada (%s) — a senha efemera sera girada no fim", motivo)
}

// autenticaPorTempo marca a sessao pelas conexoes AINDA abertas que ja passaram do
// limiar. Os fins que nao vem de um "closed" (fantasma, expiracao) nunca passam pelo
// ramo do closedRe, entao e aqui que uma sessao longa e presa se declara legitima.
func (t *tailer) autenticaPorTempo() {
	for id, abertaEm := range t.open {
		if time.Since(abertaEm) >= duracaoMinAutenticada {
			t.marcaAutenticada(fmt.Sprintf("#%s aberta ha mais de %s", id, duracaoMinAutenticada))
			return
		}
	}
}

// rotacionarSeAutenticada gira a senha efemera no fim de sessao — SO se a sessao
// chegou a autenticar. Tentativa recusada nao gira: a senha nunca foi aceita, entao
// nada vazou, e girar ali so invalidaria a senha que o painel acabou de entregar.
// Fica igual ao caso "o tecnico pediu a senha e nao conectou", que ja nao girava.
// Chamar sempre no fim REAL, que tambem e onde a marca da sessao e limpa.
func (t *tailer) rotacionarSeAutenticada(motivo string) {
	autenticada := t.autenticada
	t.autenticada = false
	if !autenticada {
		logln("ROTATE suprimido (%s): nenhuma conexao autenticou — senha do painel mantida", motivo)
		return
	}
	// Em goroutine: faz exec (--password) + HTTP e nao pode bloquear o poll de deteccao.
	go rotateNow()
}

func (t *tailer) processLine(line string) {
	if diagRe.MatchString(line) {
		logln("DIAG log: %s", strings.TrimSpace(line))
	}
	if m := openedRe.FindStringSubmatch(line); m != nil {
		id := m[1]
		if _, exists := t.open[id]; !exists {
			wasEmpty := len(t.open) == 0
			t.open[id] = time.Now()
			logln(">>> conexao #%s aberta (abertas agora: %d)", id, len(t.open))
			if wasEmpty {
				// §3.1: se havia carencia armada, este open e uma RECONEXAO da mesma
				// sessao (blip/queda curta) -> cancela a carencia e NAO re-emite start
				// (sem novo grant, sem flag de "acesso externo", sem churn de quota).
				if !t.graceUntil.IsZero() {
					t.graceUntil = time.Time{}
					logln(">>> reconexao dentro da carencia — mesma sessao (start/rotacao suprimidos)")
				} else {
					logln(">>> SESSAO INICIADA")
					// controllerID ainda vazio aqui (o peer_id vem apos o login); o
					// re-post no ramo peerIdRe leva o controlador quando ele chegar.
					if hc := postEvent("start", t.controllerID); !hc.IsZero() {
						t.hardCapUntil = hc
						logln(">>> corte de 2h (free) armado para %s", hc.Format(time.RFC3339))
					}
				}
			}
		}
		return
	}
	// peer_id: rustdesk_id do controlador desta conexao (auto-adocao). Chega apos o login,
	// depois do "opened". Se o #N esta aberto e ainda nao sabiamos o controlador da sessao,
	// guarda e RE-POSTA start com o controlador -> gatilho da auto-adocao no servidor
	// (idempotente p/ device ja adotado: a session-ingest so trata start c/ device ausente).
	if m := peerIdRe.FindStringSubmatch(line); m != nil {
		id, peer := m[1], m[2]
		// O peer_id so sai depois do login aceito: e a prova direta de que esta
		// conexao NAO e uma senha recusada. Marca antes do filtro do controllerID —
		// a 2a conexao da mesma sessao tambem autentica, e o controlador ja e conhecido.
		if _, open := t.open[id]; open {
			t.marcaAutenticada(fmt.Sprintf("peer_id do #%s", id))
		}
		if _, open := t.open[id]; open && t.controllerID == "" {
			t.controllerID = peer
			logln(">>> peer_id do #%s = %s (controlador) — reenviando start com controlador", id, peer)
			if hc := postEvent("start", t.controllerID); !hc.IsZero() {
				t.hardCapUntil = hc
			}
		}
		return
	}
	if m := closedRe.FindStringSubmatch(line); m != nil {
		id := m[1]
		if abertaEm, exists := t.open[id]; exists {
			// Rede de protecao do cliente que nao emite peer_id: uma conexao que ficou
			// aberta mais que o limiar nao foi senha recusada (essa fecha em segundos).
			if time.Since(abertaEm) >= duracaoMinAutenticada {
				t.marcaAutenticada(fmt.Sprintf("#%s durou mais de %s", id, duracaoMinAutenticada))
			}
			delete(t.open, id)
			logln("<<< conexao #%s fechada (abertas agora: %d)", id, len(t.open))
			if len(t.open) == 0 {
				// §3.1: NAO encerra/rotaciona na hora. Arma a carencia; se uma reconexao
				// chegar antes do prazo, e a mesma sessao. Se o prazo estourar vazio,
				// checkGrace() encerra (postEvent("end")) e rotaciona a senha efemera.
				t.graceUntil = time.Now().Add(graceWindow)
				logln("<<< sessao vazia — carencia de %s armada (aguarda reconexao)", graceWindow)
			}
		}
	}
}

// checkGrace dispara o fim de sessao ADIADO quando a carencia (§3.1) expira sem que
// tenha chegado uma reconexao. Chamado a cada tick do poll (granularidade ~pollInterval
// sobre graceWindow — ex.: 3s sobre 60s, aceitavel). Roda na mesma goroutine do worker.
func (t *tailer) checkGrace() {
	if t.graceUntil.IsZero() || len(t.open) > 0 {
		return
	}
	if time.Now().After(t.graceUntil) {
		t.graceUntil = time.Time{}
		t.hardCapUntil = time.Time{} // fim real -> desarma o cap 2h (nao vaza pro proximo atendimento)
		t.controllerID = ""          // fim real -> esquece o controlador desta sessao
		logln("<<< carencia expirou sem reconexao — SESSAO ENCERRADA")
		postEvent("end", "")
		// Fase 2: gira a senha efemera so agora, no fim REAL da sessao — e so se a
		// sessao chegou a autenticar (tentativa recusada nao gira).
		t.rotacionarSeAutenticada("carencia expirou")
	}
}

// checkHardCap (Billing B2/B6): corte rigido. Chamado a cada tick do poll. Cobre
// tres gatilhos, todos via hard_cap_at da session-ingest: cap de 2h do free, sem
// saldo (free+credito zerados) e teto de simultaneas por tenant (B6) — nos dois
// ultimos o servidor manda hard_cap = agora. Ao vencer com sessao aberta: desarma,
// ENCERRA a sessao (ver abaixo), rotaciona a senha e reinicia o cliente branded.
func (t *tailer) checkHardCap() {
	if t.hardCapUntil.IsZero() || len(t.open) == 0 {
		return
	}
	if time.Now().After(t.hardCapUntil) {
		t.hardCapUntil = time.Time{} // desarma ANTES de agir -> nao corta de novo no proximo tick
		logln("<<< CORTE (limite): rotacionando senha e derrubando a sessao (abertas: %d)", len(t.open))

		// Le a marca ANTES de esvaziar t.open: o corte nao passa por "closed", entao e
		// aqui que uma sessao longa sem peer_id se declara legitima. O corte de 2h so
		// vence sobre sessao real, entao na pratica isto sempre marca; o guard existe
		// pros gatilhos de saldo/simultaneas, que o servidor manda com cap = agora.
		t.autenticaPorTempo()
		autenticada := t.autenticada
		t.autenticada = false

		// A sessao acaba AGORA por corte administrativo. O force-stop do servico do
		// cliente NAO emite "#N Connection closed" no log -> os #N ficam presos em
		// t.open, o heartbeat continua e o connection_logs vira FANTASMA (ativo ate
		// expireStale/24h), alem de re-disparar o corte a cada heartbeat (rotate/
		// restart em loop). Por isso tratamos como fim REAL: esvazia o conjunto,
		// cancela a carencia e encerra a sessao no banco (end) ANTES de derrubar. Sem
		// #N aberto nao ha reconexao legitima a esperar (a conexao apos o restart
		// nasce como sessao nova, que a session-ingest re-avalia e re-corta se preciso).
		t.open = make(map[string]time.Time)
		t.graceUntil = time.Time{}
		t.controllerID = ""
		postEvent("end", "")

		go func() {
			if autenticada {
				rotateNow() // 1) nova senha no endpoint (a vista morre)
			} else {
				logln("ROTATE suprimido (corte): nenhuma conexao autenticou — senha do painel mantida")
			}
			restartClientService() // 2) reinicia o cliente -> a conexao ativa cai
		}()
	}
}

// expireStale remove #N preso ha mais de 24h. Um "closed" pode se perder numa
// rotacao de log (opened num arquivo, closed noutro); sem esta guarda o agente
// mandaria heartbeat pra sempre e o faturamento nunca fecharia a sessao. Se a
// expiracao esvaziar o conjunto, forca "end" — a duracao sai inflada, mas o
// caso e raro e filtravel por duracao no backend.
func (t *tailer) expireStale() {
	const maxAge = 24 * time.Hour
	now := time.Now()
	for id, seen := range t.open {
		if now.Sub(seen) > maxAge {
			delete(t.open, id)
			logln("WARN conexao #%s expirada (>24h sem 'closed') — forcando fim", id)
			if len(t.open) == 0 {
				t.hardCapUntil = time.Time{} // fim forcado -> desarma o cap 2h
				t.controllerID = ""
				t.autenticada = false // fim forcado -> nao vaza a marca pra proxima sessao
				postEvent("end", "")
			}
		}
	}
}

// maxPendingBytes: teto do buffer de linha parcial. Uma linha do cliente nao passa de
// alguns KB; acima disso e sinal de arquivo estranho (binario, escrita sem newline) e
// segurar aquilo indefinidamente so consumiria memoria.
const maxPendingBytes = 64 << 10

// fatiaLinhas junta a sobra da leitura anterior ao pedaco novo e devolve SO as linhas
// completas, guardando de volta o que ficou sem "\n". Sem isto o poll de 3s cortava
// linhas ao meio: os dois fragmentos passavam pelos regex sem casar e o evento (em
// especial o "#N Connection closed") sumia de vez.
func (t *tailer) fatiaLinhas(chunk string) []string {
	buf := t.pending + chunk
	t.pending = ""
	if buf == "" {
		return nil
	}
	i := strings.LastIndexByte(buf, '\n')
	if i < 0 {
		if len(buf) > maxPendingBytes {
			logln("WARN linha parcial acima de %d bytes — buffer descartado", maxPendingBytes)
			return nil
		}
		t.pending = buf // nada completo ainda; espera o proximo tick
		return nil
	}
	if resto := buf[i+1:]; len(resto) <= maxPendingBytes {
		t.pending = resto
	} else {
		logln("WARN sobra parcial acima de %d bytes — buffer descartado", maxPendingBytes)
	}
	return strings.Split(buf[:i], "\n")
}

// tailRotacionado devolve o que ficou no arquivo ANTIGO a partir de 'from'. O cliente
// rotaciona renomeando o _rCURRENT.log e criando um novo no lugar, entao o trecho
// escrito entre o nosso ultimo poll e o rename so existe no irmao renomeado. Pega o
// AcessoFast_r*.log mais recente do mesmo diretorio que nao seja o proprio _rCURRENT;
// "" quando nao ha nada a recuperar.
func tailRotacionado(cur string, from int64) string {
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(cur), clientLogPattern))
	var best string
	var bestT time.Time
	for _, m := range matches {
		if strings.EqualFold(m, cur) {
			continue
		}
		fi, err := os.Stat(m)
		if err != nil || !fi.ModTime().After(bestT) {
			continue
		}
		bestT, best = fi.ModTime(), m
	}
	// So aceita rotacionado recem-fechado: um arquivo velho aqui seria historico, e
	// reprocessar historico reabriria #N ja encerrado. 10 min cobre de sobra a distancia
	// entre o rename e o nosso poll de 3s.
	if best == "" || time.Since(bestT) > 10*time.Minute {
		return ""
	}
	fi, err := os.Stat(best)
	if err != nil || fi.Size() <= from {
		return ""
	}
	f, err := os.Open(best)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(f)
	if err != nil || len(data) == 0 {
		return ""
	}
	logln("rotacao: recuperando %d byte(s) da cauda de %s", len(data), filepath.Base(best))
	return string(data)
}

func (t *tailer) poll() {
	// Restart do cliente (reboot/crash/restart manual/corte): o processo do servico troca
	// de horario de inicio -> TODAS as conexoes cairam, e o "#N closed" NAO e logado num
	// force-stop. Sem tratar isto, o #N fica preso em t.open e a sessao vira FANTASMA
	// (heartbeat eterno; connection_logs 'active' pra sempre; painel "em andamento"; o cron
	// de 90s nunca fecha porque o heartbeat esta fresco). Detecta pelo start-time do
	// processo (sinal fora do log, que sozinho nao distingue restart de rotacao benigna) e
	// encerra a sessao. Se o peer ja reconectou, o prime abaixo abre uma sessao nova.
	if st, ok := clientProcStartTime(); ok {
		if !t.clientStart.IsZero() && st.After(t.clientStart) {
			if len(t.open) > 0 {
				logln("cliente reiniciou (%s -> %s) — conexoes cairam sem 'closed'; encerrando sessao",
					t.clientStart.Format("15:04:05"), st.Format("15:04:05"))
				t.open = make(map[string]time.Time)
				t.graceUntil = time.Time{}
				t.hardCapUntil = time.Time{}
				t.controllerID = ""
				t.autenticada = false // fim real -> nao vaza a marca pra sessao seguinte
				postEvent("end", "")
			}
			t.primed = false // re-prima do log novo
			t.offset = 0
			// O cliente subiu de novo lendo a config do disco: a senha que ele passa a
			// exigir pode nao ser a que o painel conhece. Re-sincroniza (o rotateNow so
			// aplica com o cliente de pe, e aqui ele acabou de subir).
			go rotateAposRestartDoCliente()
		}
		t.clientStart = st
	}

	p := findRustdeskLog()
	if p == "" {
		return
	}
	if p != t.path {
		logln("log do cliente: %s", p)
		t.path = p
		t.offset = 0
		t.primed = false
		t.pending = ""
	}
	fi, err := os.Stat(p)
	if err != nil {
		return
	}
	if fi.Size() < t.offset { // rotacionou/truncou
		// A cauda do arquivo ANTIGO (o que o cliente escreveu entre o ultimo poll e o
		// rename) sairia de cena junto com ele. E exatamente ali que um "#N Connection
		// closed" se perde e a sessao vira fantasma — entao recuperamos antes de zerar.
		// Nao primado nao chega aqui (offset 0 nunca e maior que o tamanho).
		if t.primed {
			for _, ln := range t.fatiaLinhas(tailRotacionado(p, t.offset)) {
				if ln != "" {
					t.processLine(ln)
				}
			}
		}
		t.offset = 0
		t.pending = "" // o resto parcial era do arquivo antigo
		// NAO limpa t.open nem re-prima: as conexoes abertas atravessam a
		// rotacao; so voltamos a ler o novo arquivo do inicio.
		logln("log rotacionou; relendo do novo arquivo (abertas: %d)", len(t.open))
	}
	f, err := os.Open(p)
	if err != nil {
		return
	}
	defer f.Close()
	f.Seek(t.offset, io.SeekStart)
	data, _ := io.ReadAll(f)
	t.offset += int64(len(data))

	lines := t.fatiaLinhas(string(data))
	if !t.primed {
		// Reconstrucao de estado no boot: reproduz opened/closed do arquivo
		// atual SEM postar eventos historicos. Se sobrar #N aberto, ha sessao
		// ativa agora -> um unico "start". Tambem parseia peer_id: se a linha do
		// controlador da conexao ainda-aberta esta NESTE log, aprende o controlador
		// aqui pra o start do prime ja o levar.
		for _, ln := range lines {
			if m := openedRe.FindStringSubmatch(ln); m != nil {
				t.open[m[1]] = time.Now()
			} else if m := closedRe.FindStringSubmatch(ln); m != nil {
				delete(t.open, m[1])
			} else if m := peerIdRe.FindStringSubmatch(ln); m != nil {
				// SEGURANCA: guarda o controlador de uma conexao ainda ABERTA no boot.
				// Sem isto, uma sessao capturada pelo prime (reconexao do invasor DURANTE
				// o restart do corte) ia sem controller_rustdesk_id -> so 404, nunca 403
				// unknown_controller -> ESCAPAVA do corte por ~20-44s (visto em teste real).
				// Com o controlador, o start do prime dispara o 403 -> corte em ~3s. A
				// ultima linha peer_id de um #N aberto vence (sessao corrente).
				if _, open := t.open[m[1]]; open {
					t.controllerID = m[2]
				}
			}
		}
		t.primed = true
		if len(t.open) > 0 {
			logln("prime: %d conexao(oes) aberta(s) no boot -> enviando start (controlador=%q)", len(t.open), t.controllerID)
			// Se aprendemos o controlador da sessao aberta (linha peer_id no log atual), o
			// start ja o leva -> auto-adocao (device novo legitimo) OU corte (controlador nao
			// adotado). Sem peer_id no log (device ja adotado, reboot comum) segue sem
			// controlador, que basta (o servidor ignora o controlador p/ device ja adotado).
			if hc := postEvent("start", t.controllerID); !hc.IsZero() {
				t.hardCapUntil = hc
				logln(">>> corte armado para %s", hc.Format(time.RFC3339))
			}
		} else {
			logln("prime: sem sessao ativa no boot")
		}
		return
	}
	for _, ln := range lines {
		if ln != "" {
			t.processLine(ln)
		}
	}
	t.expireStale()
}

func worker(stop <-chan struct{}) {
	logln("===== AcessoFast agent iniciado =====")
	token = readTrim(tokenFile)
	if token == "" {
		logln("sem token em %s -> MODO MATRICULA (tailer ativo p/ auto-adocao)", tokenFile)
		// NAO bloqueia: seta o token PENDENTE (+ rustdeskID) e registra o claim,
		// depois segue pro loop de sessao com o tailer LIGADO. Assim um acesso
		// direto pode postar o 'start' com controller_rustdesk_id que dispara a
		// auto-adocao no servidor. A credencial e persistida por uma goroutine
		// (claim-status) quando a maquina for adotada — manual (painel) ou direto.
		// Sem isto havia deadlock: o agente esperava o claim; o claim esperava o
		// 'start' que so o tailer (desligado) mandaria.
		if !startMatricula(stop) {
			logln("===== agent parando (matricula interrompida) =====")
			return
		}
		// token e rustdeskID ja setados (token pendente) por startMatricula.
	} else {
		rustdeskID = discoverRustdeskID()
	}
	logln("token carregado (len=%d)", len(token))
	if rustdeskID == "" {
		logln("ERRO: rustdesk_id nao encontrado (nem %s nem config do cliente)", ridFile)
	} else {
		logln("rustdesk_id = %s", rustdeskID)
	}

	// Fase 2: reenvia qualquer senha pendente (report que nao teve 200) ate o painel confirmar.
	go rotateRetryLoop(stop)

	// Fase 3 §3.2: rotaciona 1x no boot do agente. O timer de carencia (§3.1) vive em
	// memoria e NAO sobrevive a um reboot — sem isto, a senha vista antes do reboot
	// continuaria valida (furo R2a/#8). Suprimido sob hold de manutencao (reboot
	// planejado, §4.3). Goroutine: faz exec (--password) + HTTP, nao bloqueia o startup.
	go rotateOnBoot()

	t := &tailer{open: make(map[string]time.Time)}
	pollT := time.NewTicker(pollInterval)
	hbT := time.NewTicker(heartbeatInterval)
	presT := time.NewTicker(presenceInterval)
	defer pollT.Stop()
	defer hbT.Stop()
	defer presT.Stop()

	t.poll()

	for {
		select {
		case <-stop:
			logln("===== agent parando =====")
			return
		case <-pollT.C:
			t.poll()
			t.checkClienteParado() // autocura: sobe o servico do cliente se ele caiu
			t.checkSessaoViva()    // fantasma: #N preso sem socket de sessao -> encerra
			t.checkGrace()         // §3.1: fecha a sessao adiada quando a carencia expira
			t.checkHardCap()       // B2: corta o free ao vencer o cap de 2h
		case <-hbT.C:
			if len(t.open) > 0 {
				// Re-arma o cap se a resposta trouxer hard_cap_at; falha transitoria de
				// heartbeat NAO desarma (so o fim real limpa) — evita perder o corte.
				if hc := postEvent("heartbeat", ""); !hc.IsZero() {
					t.hardCapUntil = hc
				}
			}
		case <-presT.C:
			// Durante a carencia (§3.1) a sessao ainda nao terminou de fato -> nao
			// mandar "presence" (que so vale pra maquina comprovadamente ociosa).
			if len(t.open) == 0 && t.graceUntil.IsZero() {
				// Passo 2: 'presence' e o unico evento que pode trazer update, porque e
				// a unica prova de que a maquina esta ociosa. aplicaUpdate roda inline
				// (nao em goroutine) de proposito: assim a troca do binario nunca corre
				// em paralelo com outro tick de presence.
				if _, upd := postEventFull("presence", ""); upd != nil {
					aplicaUpdate(upd)
				}
			}
		}
	}
}

func openLog() {
	os.MkdirAll(baseDir, 0755)
	if f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
		logFH = f
	}
}

func main() {
	// --enroll intercepta ANTES de decidir servico/console: a matricula e um
	// caminho one-shot, roda e sai. flag.Parse consome --secret/--alias tambem.
	var (
		enrollMode = flag.Bool("enroll", false, "executa a matricula do endpoint e sai")
		secret     = flag.String("secret", "", "codigo da empresa (segredo de matricula do tenant)")
		alias      = flag.String("alias", "", "nome amigavel da maquina (default: hostname)")
	)
	flag.Parse()

	if *enrollMode {
		// Matricula loga em stdout (o instalador captura); nao abre o agent.log.
		os.Exit(RunEnroll(*secret, *alias))
	}

	openLog()
	// runAgent vive na camada de plataforma: no Windows decide entre servico SCM e
	// console; no macOS roda em foreground sob o launchd, tratando SIGTERM.
	runAgent()
}
