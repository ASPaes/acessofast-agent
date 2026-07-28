// AcessoFast — Agente de Sessao + Matriculador (binario unico).
//
// Sem argumentos          -> roda como servico Windows (ou console em debug).
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
// Loga em C:\ProgramData\AcessoFast\agent.log.
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

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// ---------------------------------------------------------------------------
// Constantes e estado compartilhados (FONTE UNICA — enroll.go usa estes)
// ---------------------------------------------------------------------------

const (
	serviceName       = "AcessoFastAgent"
	baseDir           = `C:\ProgramData\AcessoFast`
	tokenFile         = baseDir + `\agent.token`
	ridFile           = baseDir + `\rustdesk_id`
	logFile           = baseDir + `\agent.log`
	ingestURL         = "https://plmfyibyrowbgjjyblcl.supabase.co/functions/v1/session-ingest"
	pollInterval      = 3 * time.Second
	heartbeatInterval = 20 * time.Second
	presenceInterval  = 60 * time.Second

	// Fase 3 §3.1 — janela de carencia (debounce) do fim de sessao. Ao esvaziar o
	// conjunto de #N, NAO encerra/rotaciona na hora: espera este prazo por uma
	// reconexao (blip/queda curta, validado no teste #3). Reconexao dentro do prazo =
	// mesma sessao (sem end, sem rotacao, sem churn de quota). Prazo vazio = fim real.
	graceWindow = 60 * time.Second
)

// anonKey e publica por design (role=anon). Preferencia: injetar no CI via
//
//	go build -ldflags "-X main.anonKey=<ANON_KEY>"
//
// O fallback hardcoded preserva o comportamento atual do agente caso o build
// nao injete (o binario que ja roda em producao tem a chave embutida).
var anonKey = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6InBsbWZ5aWJ5cm93YmdqanlibGNsIiwicm9sZSI6ImFub24iLCJpYXQiOjE3ODM2NDMyNjIsImV4cCI6MjA5OTIxOTI2Mn0.grcQYqN3fHvFTWI0AFPWG66k1wONuGqZ5yMt07qcjxE"

var (
	// Marcadores reais confirmados em log de producao (connection.rs):
	//   #619 Connection opened from 189.4.111.147:12288.
	//   #619 Connection closed: Peer close
	openedRe = regexp.MustCompile(`#(\d+) Connection opened`)
	closedRe = regexp.MustCompile(`#(\d+) Connection closed`)
	diagRe   = regexp.MustCompile(`(?i)Connection opened|Connection closed|new client|LoginRequest|authorized`)

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
	globs := []string{
		`C:\Windows\ServiceProfiles\LocalService\AppData\Roaming\AcessoFast\config\AcessoFast.toml`,
		`C:\Windows\ServiceProfiles\LocalService\AppData\Roaming\AcessoFast\config\AcessoFast2.toml`,
		`C:\Users\*\AppData\Roaming\AcessoFast\config\AcessoFast.toml`,
		`C:\Users\*\AppData\Roaming\AcessoFast\config\AcessoFast2.toml`,
	}
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
	serverLogs := []string{
		`C:\Windows\ServiceProfiles\LocalService\AppData\Roaming\AcessoFast\log\server\AcessoFast_rCURRENT.log`,
		`C:\Windows\System32\config\systemprofile\AppData\Roaming\AcessoFast\log\server\AcessoFast_rCURRENT.log`,
		`C:\Users\*\AppData\Roaming\AcessoFast\log\server\AcessoFast_rCURRENT.log`,
	}
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
}

// postEvent posta um evento de sessao e devolve o hard_cap_at da resposta (zero se
// ausente/ilegivel/erro). So start/heartbeat carregam cap; end/presence retornam zero.
func postEvent(event string) time.Time {
	// Guarda: sem credencial, nao adianta postar — a session-ingest rejeitaria.
	// Evita ruido de POST invalido a cada 60s quando a matricula ainda nao rodou.
	if token == "" || rustdeskID == "" {
		logln("SKIP %s: token/rustdesk_id ausente (matricula pendente?)", event)
		return time.Time{}
	}
	payload, _ := json.Marshal(map[string]string{
		"rustdesk_id": rustdeskID, "agent_token": token, "event": event,
	})
	req, err := http.NewRequest("POST", ingestURL, bytes.NewReader(payload))
	if err != nil {
		logln("POST %s erro ao montar req: %v", event, err)
		return time.Time{}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", anonKey)
	req.Header.Set("Authorization", "Bearer "+anonKey)
	resp, err := httpClient.Do(req)
	if err != nil {
		logln("POST %s FALHOU: %v", event, err)
		return time.Time{}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
	logln("POST %s -> HTTP %d  %s", event, resp.StatusCode, strings.TrimSpace(string(body)))

	// Billing B2: extrai o hard_cap_at (se veio). Tolera fracao de segundo do timestamptz.
	var r ingestResp
	if json.Unmarshal(body, &r) == nil && r.HardCapAt != "" {
		if ts, e := time.Parse(time.RFC3339, r.HardCapAt); e == nil {
			return ts
		}
		if ts, e := time.Parse(time.RFC3339Nano, r.HardCapAt); e == nil {
			return ts
		}
		logln("WARN hard_cap_at ilegivel: %q", r.HardCapAt)
	}
	return time.Time{}
}

// tailer acompanha o log e mantem o conjunto de conexoes (#N) abertas.
// active == len(open) > 0. Estado sequencial: so e tocado dentro do select do
// worker (uma unica goroutine) — sem necessidade de mutex.
type tailer struct {
	path   string
	offset int64
	primed bool
	open   map[string]time.Time // #N aberto -> instante em que vimos o "opened"

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
}

// clientProcStartTime devolve o horario de inicio do processo do servico do cliente
// branded (AcessoFast). Fonte FORA do log — o log nao distingue restart (conexoes
// cairam) de rotacao benigna (conexoes seguem). (time.Time{}, false) se indisponivel
// (servico parado, sem permissao): nesse caso o detector nao age.
func clientProcStartTime() (time.Time, bool) {
	m, err := mgr.Connect()
	if err != nil {
		return time.Time{}, false
	}
	defer m.Disconnect()
	s, err := m.OpenService(clientServiceName)
	if err != nil {
		return time.Time{}, false
	}
	defer s.Close()
	st, err := s.Query()
	if err != nil || st.ProcessId == 0 {
		return time.Time{}, false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, st.ProcessId)
	if err != nil {
		return time.Time{}, false
	}
	defer windows.CloseHandle(h)
	var cre, exi, ker, usr windows.Filetime
	if err := windows.GetProcessTimes(h, &cre, &exi, &ker, &usr); err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, cre.Nanoseconds()), true
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
					if hc := postEvent("start"); !hc.IsZero() {
						t.hardCapUntil = hc
						logln(">>> corte de 2h (free) armado para %s", hc.Format(time.RFC3339))
					}
				}
			}
		}
		return
	}
	if m := closedRe.FindStringSubmatch(line); m != nil {
		id := m[1]
		if _, exists := t.open[id]; exists {
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
		logln("<<< carencia expirou sem reconexao — SESSAO ENCERRADA")
		postEvent("end")
		// Fase 2: gira a senha efemera so agora, no fim REAL da sessao. Em goroutine —
		// faz exec (--password) + HTTP e nao pode bloquear o poll de deteccao.
		go rotateNow()
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
		postEvent("end")

		go func() {
			rotateNow()            // 1) nova senha no endpoint (a vista morre)
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
				postEvent("end")
			}
		}
	}
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
				postEvent("end")
			}
			t.primed = false // re-prima do log novo
			t.offset = 0
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
	}
	fi, err := os.Stat(p)
	if err != nil {
		return
	}
	if fi.Size() < t.offset { // rotacionou/truncou
		t.offset = 0
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

	lines := strings.Split(string(data), "\n")
	if !t.primed {
		// Reconstrucao de estado no boot: reproduz opened/closed do arquivo
		// atual SEM postar eventos historicos. Se sobrar #N aberto, ha sessao
		// ativa agora -> um unico "start".
		for _, ln := range lines {
			if m := openedRe.FindStringSubmatch(ln); m != nil {
				t.open[m[1]] = time.Now()
			} else if m := closedRe.FindStringSubmatch(ln); m != nil {
				delete(t.open, m[1])
			}
		}
		t.primed = true
		if len(t.open) > 0 {
			logln("prime: %d conexao(oes) aberta(s) no boot -> enviando start", len(t.open))
			if hc := postEvent("start"); !hc.IsZero() {
				t.hardCapUntil = hc
				logln(">>> corte de 2h (free) armado para %s", hc.Format(time.RFC3339))
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
		logln("sem token em %s -> MODO MATRICULA", tokenFile)
		if !runMatricula(stop) {
			logln("===== agent parando (matricula interrompida) =====")
			return
		}
		token = readTrim(tokenFile) // re-le o token ja confirmado
	}
	logln("token carregado (len=%d)", len(token))
	rustdeskID = discoverRustdeskID()
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
			t.checkGrace()   // §3.1: fecha a sessao adiada quando a carencia expira
			t.checkHardCap() // B2: corta o free ao vencer o cap de 2h
		case <-hbT.C:
			if len(t.open) > 0 {
				// Re-arma o cap se a resposta trouxer hard_cap_at; falha transitoria de
				// heartbeat NAO desarma (so o fim real limpa) — evita perder o corte.
				if hc := postEvent("heartbeat"); !hc.IsZero() {
					t.hardCapUntil = hc
				}
			}
		case <-presT.C:
			// Durante a carencia (§3.1) a sessao ainda nao terminou de fato -> nao
			// mandar "presence" (que so vale pra maquina comprovadamente ociosa).
			if len(t.open) == 0 && t.graceUntil.IsZero() {
				postEvent("presence")
			}
		}
	}
}

// ---- servico Windows ----
type service struct{}

func (s *service) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}
	stop := make(chan struct{})
	go worker(stop)
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			close(stop)
			changes <- svc.Status{State: svc.StopPending}
			return false, 0
		default:
		}
	}
	return false, 0
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
	isSvc, err := svc.IsWindowsService()
	if err != nil {
		logln("IsWindowsService erro: %v", err)
	}
	if !isSvc {
		logln("(modo console/debug — Ctrl+C pra sair)")
		worker(make(chan struct{}))
		return
	}
	if err := svc.Run(serviceName, &service{}); err != nil {
		logln("svc.Run erro: %v", err)
	}
}
