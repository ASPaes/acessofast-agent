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

	"golang.org/x/sys/windows/svc"
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

// findRustdeskLog aponta para o log de SESSAO do cliente branded. Confirmado em
// maquina real: o namespace segue o app-name (AcessoFast_rCURRENT.log), sob a
// conta LocalService (onde o servico do cliente roda), na subpasta "server" —
// que e onde os eventos "#N Connection opened/closed" sao escritos.
func findRustdeskLog() string {
	patterns := []string{
		`C:\Windows\ServiceProfiles\LocalService\AppData\Roaming\AcessoFast\log\server\AcessoFast_rCURRENT.log`,
		`C:\Windows\ServiceProfiles\LocalService\AppData\Roaming\AcessoFast\log\AcessoFast_rCURRENT.log`,
		`C:\Users\*\AppData\Roaming\AcessoFast\log\server\AcessoFast_rCURRENT.log`,
		`C:\Users\*\AppData\Roaming\AcessoFast\log\AcessoFast_rCURRENT.log`,
	}
	var best string
	var bestT time.Time
	for _, p := range patterns {
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

func postEvent(event string) {
	// Guarda: sem credencial, nao adianta postar — a session-ingest rejeitaria.
	// Evita ruido de POST invalido a cada 60s quando a matricula ainda nao rodou.
	if token == "" || rustdeskID == "" {
		logln("SKIP %s: token/rustdesk_id ausente (matricula pendente?)", event)
		return
	}
	payload, _ := json.Marshal(map[string]string{
		"rustdesk_id": rustdeskID, "agent_token": token, "event": event,
	})
	req, err := http.NewRequest("POST", ingestURL, bytes.NewReader(payload))
	if err != nil {
		logln("POST %s erro ao montar req: %v", event, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", anonKey)
	req.Header.Set("Authorization", "Bearer "+anonKey)
	resp, err := httpClient.Do(req)
	if err != nil {
		logln("POST %s FALHOU: %v", event, err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
	logln("POST %s -> HTTP %d  %s", event, resp.StatusCode, strings.TrimSpace(string(body)))
}

// tailer acompanha o log e mantem o conjunto de conexoes (#N) abertas.
// active == len(open) > 0. Estado sequencial: so e tocado dentro do select do
// worker (uma unica goroutine) — sem necessidade de mutex.
type tailer struct {
	path   string
	offset int64
	primed bool
	open   map[string]time.Time // #N aberto -> instante em que vimos o "opened"
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
				logln(">>> SESSAO INICIADA")
				postEvent("start")
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
				logln("<<< SESSAO ENCERRADA")
				postEvent("end")
			}
		}
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
				postEvent("end")
			}
		}
	}
}

func (t *tailer) poll() {
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
			postEvent("start")
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
		logln("ERRO: token vazio/ausente em %s", tokenFile)
	} else {
		logln("token carregado (len=%d)", len(token))
	}
	rustdeskID = discoverRustdeskID()
	if rustdeskID == "" {
		logln("ERRO: rustdesk_id nao encontrado (nem %s nem config do cliente)", ridFile)
	} else {
		logln("rustdesk_id = %s", rustdeskID)
	}

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
		case <-hbT.C:
			if len(t.open) > 0 {
				postEvent("heartbeat")
			}
		case <-presT.C:
			if len(t.open) == 0 {
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
