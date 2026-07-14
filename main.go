// AcessoFast — Agente de Sessao + Matriculador (binario unico).
//
// Sem argumentos          -> roda como servico Windows (ou console em debug).
// Com --enroll            -> executa a matricula do endpoint UMA vez e sai.
//
// O agente detecta sessao RustDesk lendo o "connection count" do log
// rustdesk_rCURRENT.log e reporta start/heartbeat/end para a Edge Function
// session-ingest. Envia "presence" quando ocioso (so atualiza last_online).
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
	"strconv"
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
	countRe = regexp.MustCompile(`connection count:\s*(\d+)`)
	diagRe  = regexp.MustCompile(`(?i)connection count|new client|close|closed|offline|disconnect|stop video|exit .*service|LoginRequest|authorized`)

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
// varre a config do RustDesk procurando o campo id.
func discoverRustdeskID() string {
	if v := readTrim(ridFile); v != "" {
		return v
	}
	globs := []string{
		`C:\Users\*\AppData\Roaming\RustDesk\config\RustDesk.toml`,
		`C:\Users\*\AppData\Roaming\RustDesk\config\RustDesk2.toml`,
		`C:\Windows\ServiceProfiles\LocalService\AppData\Roaming\RustDesk\config\RustDesk.toml`,
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

func findRustdeskLog() string {
	patterns := []string{
		`C:\Windows\ServiceProfiles\LocalService\AppData\Roaming\RustDesk\log\rustdesk_rCURRENT.log`,
		`C:\Windows\ServiceProfiles\LocalService\AppData\Roaming\RustDesk\log\server\rustdesk_rCURRENT.log`,
		`C:\Users\*\AppData\Roaming\RustDesk\log\rustdesk_rCURRENT.log`,
		`C:\Users\*\AppData\Local\rustdesk\log\rustdesk_rCURRENT.log`,
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

type tailer struct {
	path   string
	offset int64
	primed bool
	active bool
}

func (t *tailer) processLine(line string) {
	if diagRe.MatchString(line) {
		logln("DIAG log: %s", strings.TrimSpace(line))
	}
	m := countRe.FindStringSubmatch(line)
	if m == nil {
		return
	}
	cnt, _ := strconv.Atoi(m[1])
	if cnt >= 1 && !t.active {
		t.active = true
		logln(">>> SESSAO INICIADA (connection count = %d)", cnt)
		postEvent("start")
	} else if cnt == 0 && t.active {
		t.active = false
		logln("<<< SESSAO ENCERRADA (connection count = 0)")
		postEvent("end")
	}
}

func (t *tailer) poll() {
	p := findRustdeskLog()
	if p == "" {
		return
	}
	if p != t.path {
		logln("log RustDesk: %s", p)
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
		t.primed = false
		logln("log rotacionou, relendo do inicio")
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
		// Prime: estado atual pelo ULTIMO connection count, sem disparar eventos historicos.
		last := -1
		for _, ln := range lines {
			if m := countRe.FindStringSubmatch(ln); m != nil {
				last, _ = strconv.Atoi(m[1])
			}
		}
		t.primed = true
		if last >= 1 {
			t.active = true
			logln("prime: sessao JA ativa no boot (count=%d) -> enviando start", last)
			postEvent("start")
		} else {
			logln("prime: sem sessao ativa (count=%d)", last)
		}
		return
	}
	for _, ln := range lines {
		if ln != "" {
			t.processLine(ln)
		}
	}
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
		logln("ERRO: rustdesk_id nao encontrado (nem %s nem config do RustDesk)", ridFile)
	} else {
		logln("rustdesk_id = %s", rustdeskID)
	}

	t := &tailer{}
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
			if t.active {
				postEvent("heartbeat")
			}
		case <-presT.C:
			if !t.active {
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
