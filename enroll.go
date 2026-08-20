// AcessoFast — enroll.go — Modulo de Matricula do Endpoint
//
// Roda em modo ONE-SHOT, chamado pelo instalador (Inno Setup):
//
//	acessofast-agent.exe --enroll --secret=<CODIGO_DA_EMPRESA> [--alias=<NOME>]
//
// Fluxo:
//  1. Localiza o AcessoFast.exe (cliente branded) instalado
//  2. Le o RustDesk ID (--get-id, com retry: o ID so existe apos o 1o run do servico)
//  3. POST /functions/v1/enroll-device {secret, rustdesk_id, os, alias}
//  4. Grava agent.token + rustdesk_id em C:\ProgramData\AcessoFast com ACL RESTRITA
//
// Exit codes (o Inno Setup usa para dar mensagem especifica ao usuario):
//
//	0 = matriculado com sucesso
//	2 = segredo invalido ou revogado (HTTP 401)
//	3 = falha de rede (sem internet / relay inalcancavel)
//	4 = RustDesk nao encontrado ou ID indisponivel
//	5 = falha ao gravar credenciais em disco
//	1 = erro generico
//
// Parte do BINARIO UNICO acessofast-agent.exe: o main.go decide, pela flag
// --enroll, se roda o servico (default) ou esta matricula one-shot. As constantes
// compartilhadas (baseDir, tokenFile, ridFile, anonKey) vivem em main.go — fonte unica.
// Os paths de credencial (C:\ProgramData\AcessoFast) batem com o agente que ja roda
// em producao. O path do cliente foi corrigido para o namespace branded AcessoFast
// (confirmado em maquina real: C:\Program Files\AcessoFast\AcessoFast.exe).
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const (
	enrollURL = "https://plmfyibyrowbgjjyblcl.supabase.co/functions/v1/enroll-device"

	// O ID do RustDesk so e gerado no 1o run do servico. Damos tempo.
	getIDTimeout  = 90 * time.Second
	getIDInterval = 2 * time.Second

	enrollHTTPTimeout = 25 * time.Second
)

// Exit codes — contrato com o instalador.
const (
	exitOK           = 0
	exitGeneric      = 1
	exitBadSecret    = 2
	exitNetwork      = 3
	exitNoRustDeskID = 4
	exitWriteFailed  = 5
)

// O RustDesk ja produziu ID com espacos ("1 474 224 253") — isso ja mordeu em producao.
// Normalizamos no cliente tambem, alem do que a Edge Function faz.
var ridRe = regexp.MustCompile(`^\d{6,12}$`)

type enrollReq struct {
	Secret     string `json:"secret"`
	RustDeskID string `json:"rustdesk_id"`
	OS         string `json:"os,omitempty"`
	Alias      string `json:"alias,omitempty"`
}

type enrollResp struct {
	OK               bool   `json:"ok"`
	DeviceID         string `json:"device_id"`
	EnrollmentStatus string `json:"enrollment_status"`
	AgentToken       string `json:"agent_token"`
	Error            string `json:"error"`
	Detail           string `json:"detail"`
}

// enrollExitError carrega o exit code que o instalador deve receber.
type enrollExitError struct {
	code int
	msg  string
}

func (e *enrollExitError) Error() string { return e.msg }

func failWith(code int, format string, a ...any) *enrollExitError {
	return &enrollExitError{code: code, msg: fmt.Sprintf(format, a...)}
}

// Localizacao do cliente branded: findRustDeskExe() vive na camada de plataforma
// (plat_windows.go / plat_darwin.go) — o caminho de instalacao nao tem nada em comum
// entre um Program Files e um bundle .app.

// ---------------------------------------------------------------------------
// Leitura do RustDesk ID (com retry — o ID nasce no 1o run do servico)
// ---------------------------------------------------------------------------

func getRustDeskID(exe string) (string, error) {
	deadline := time.Now().Add(getIDTimeout)
	var lastRaw string

	for time.Now().Before(deadline) {
		out, err := exec.Command(exe, "--get-id").Output()
		if err == nil {
			// Normaliza: remove TODO espaco em branco (bug conhecido do "1 474 224 253").
			id := strings.Join(strings.Fields(string(out)), "")
			lastRaw = id
			if ridRe.MatchString(id) {
				return id, nil
			}
		}
		time.Sleep(getIDInterval)
	}

	if lastRaw == "" {
		return "", fmt.Errorf("--get-id nao retornou nada em %s", getIDTimeout)
	}
	return "", fmt.Errorf("--get-id retornou valor invalido (%q) em %s", lastRaw, getIDTimeout)
}

// ---------------------------------------------------------------------------
// Identificacao do SO  (osString() e por plataforma)
// ---------------------------------------------------------------------------

func machineAlias() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return ""
}

// ---------------------------------------------------------------------------
// Chamada da Edge Function
// ---------------------------------------------------------------------------

func postEnroll(secret, rid, osStr, alias string) (*enrollResp, error) {
	payload, err := json.Marshal(enrollReq{
		Secret:     secret,
		RustDeskID: rid,
		OS:         osStr,
		Alias:      alias,
	})
	if err != nil {
		return nil, failWith(exitGeneric, "falha ao montar payload: %v", err)
	}

	req, err := http.NewRequest("POST", enrollURL, bytes.NewReader(payload))
	if err != nil {
		return nil, failWith(exitGeneric, "falha ao montar request: %v", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("apikey", anonKey)
	req.Header.Set("authorization", "Bearer "+anonKey)

	client := &http.Client{Timeout: enrollHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) {
			return nil, failWith(exitNetwork, "sem conexao com o servidor AcessoFast: %v", err)
		}
		return nil, failWith(exitNetwork, "falha de rede: %v", err)
	}
	defer resp.Body.Close()

	var out enrollResp
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&out); err != nil {
		return nil, failWith(exitGeneric, "resposta ilegivel do servidor (HTTP %d)", resp.StatusCode)
	}

	switch {
	case resp.StatusCode == 401 || out.Error == "invalid_or_revoked_secret":
		return nil, failWith(exitBadSecret, "codigo da empresa invalido ou revogado")
	case resp.StatusCode >= 500:
		return nil, failWith(exitNetwork, "servidor AcessoFast indisponivel (HTTP %d): %s", resp.StatusCode, out.Detail)
	case resp.StatusCode != 200 || !out.OK:
		return nil, failWith(exitGeneric, "matricula recusada (HTTP %d): %s", resp.StatusCode, out.Error)
	case out.AgentToken == "":
		return nil, failWith(exitGeneric, "servidor nao devolveu token de agente")
	}

	return &out, nil
}

// ---------------------------------------------------------------------------
// Gravacao segura das credenciais
// ---------------------------------------------------------------------------

// hardenDir (por plataforma) tranca o diretorio de credenciais para o administrador
// do sistema. O agent.token e uma CREDENCIAL: quem o le forja eventos de sessao
// daquela maquina e corrompe o faturamento. Nos dois SOs o diretorio publico e
// legivel por usuario comum por padrao, entao trancar nao e opcional.

func writeCredentials(token, rid string) error {
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return fmt.Errorf("nao foi possivel criar %s: %v", baseDir, err)
	}

	// Tranca ANTES de escrever o segredo — evita janela de leitura por usuario comum.
	if err := hardenDir(baseDir); err != nil {
		return err
	}

	if err := os.WriteFile(tokenFile, []byte(token), 0o600); err != nil {
		return fmt.Errorf("nao foi possivel gravar %s: %v", tokenFile, err)
	}
	if err := os.WriteFile(ridFile, []byte(rid), 0o600); err != nil {
		return fmt.Errorf("nao foi possivel gravar %s: %v", ridFile, err)
	}

	// Reaplica: arquivos novos herdam do diretorio, mas confirmamos por garantia.
	if err := hardenDir(baseDir); err != nil {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Entry point do modo --enroll
// ---------------------------------------------------------------------------

// RunEnroll executa a matricula e devolve o exit code que o instalador consome.
// Toda saida vai para STDOUT — o Inno captura e loga.
func RunEnroll(secret, alias string) int {
	fmt.Println("AcessoFast — matriculando este computador...")

	secret = strings.TrimSpace(secret)
	if secret == "" {
		fmt.Println("ERRO: codigo da empresa nao informado.")
		return exitBadSecret
	}

	exe, err := findRustDeskExe()
	if err != nil {
		fmt.Printf("ERRO: %v\n", err)
		return exitNoRustDeskID
	}
	fmt.Printf("  Cliente AcessoFast localizado: %s\n", exe)

	fmt.Println("  Aguardando o ID desta maquina ser gerado...")
	rid, err := getRustDeskID(exe)
	if err != nil {
		fmt.Printf("ERRO: %v\n", err)
		return exitNoRustDeskID
	}
	fmt.Printf("  ID desta maquina: %s\n", rid)

	if alias == "" {
		alias = machineAlias()
	}

	fmt.Println("  Contatando o servidor AcessoFast...")
	resp, err := postEnroll(secret, rid, osString(), alias)
	if err != nil {
		var ee *enrollExitError
		if errors.As(err, &ee) {
			fmt.Printf("ERRO: %s\n", ee.msg)
			return ee.code
		}
		fmt.Printf("ERRO: %v\n", err)
		return exitGeneric
	}

	if err := writeCredentials(resp.AgentToken, rid); err != nil {
		fmt.Printf("ERRO: %v\n", err)
		return exitWriteFailed
	}

	fmt.Printf("  Matriculado. Status: %s\n", resp.EnrollmentStatus)
	if resp.EnrollmentStatus == "pending" {
		fmt.Println("  Este computador aguarda APROVACAO do administrador no painel.")
	}
	fmt.Println("OK")
	return exitOK
}
