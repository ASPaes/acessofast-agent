// AcessoFast — matricula.go — MODO MATRICULA (handshake por nonce, fluxo B).
//
// Roda DENTRO do servico quando o agente sobe SEM token (instalador generico,
// cliente nao digitou codigo). A maquina prova que e ela por um NONCE que so
// ela tem; o servidor nunca ve nonce nem token, so os hashes.
//
// Fluxo:
//   1. Espera o RustDesk ID existir (o cliente pode ainda estar subindo).
//   2. Gera nonce + token na maquina e PERSISTE em enroll.state (restart nao
//      cria pedido novo -> o hash do token permanece consistente com a adocao).
//   3. claim-register: cria o pedido de adocao pendente (so hashes).
//   4. claim-status em loop, provando o nonce: 'waiting' espera; 'approved'/
//      'consumed' -> grava o token confirmado (agent.token, ACL do enroll.go) e
//      vira modo sessao; 'expired'/'rejected'/'unknown' -> re-registra.
//
// Reusa de enroll.go/main.go: getRustDeskID, findRustDeskExe, discoverRustdeskID,
// machineAlias, osString, writeCredentials (ACL por SID), hardenDir, anonKey,
// baseDir, httpClient, logln. NADA do modo sessao e alterado.
package main

import (
	"bytes"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	claimRegisterURL  = "https://plmfyibyrowbgjjyblcl.supabase.co/functions/v1/claim-register"
	claimStatusURL    = "https://plmfyibyrowbgjjyblcl.supabase.co/functions/v1/claim-status"
	enrollStateFile   = baseDir + `\enroll.state` // {nonce, token} persistido durante a matricula
	claimPollInterval = 15 * time.Second
)

// enrollState e persistido em disco (ACL restrita) para sobreviver a restart:
// mesmo nonce+token -> mesmo pedido -> o hash adotado bate com o token final.
type enrollState struct {
	Nonce string `json:"nonce"` // prova de posse (nunca sai da maquina, exceto no poll TLS)
	Token string `json:"token"` // vira agent.token na adocao; so o hash e registrado
}

func randB64URL(n int) string {
	b := make([]byte, n)
	_, _ = crand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func writeEnrollState(st enrollState) error {
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return err
	}
	_ = hardenDir(baseDir) // reusa o ACL do enroll.go (SYSTEM + Admins, por SID)
	raw, _ := json.Marshal(st)
	if err := os.WriteFile(enrollStateFile, raw, 0o600); err != nil {
		return err
	}
	_ = hardenDir(baseDir)
	return nil
}

// loadOrCreateState reusa o estado persistido ou cria e grava um novo.
func loadOrCreateState() enrollState {
	if raw, err := os.ReadFile(enrollStateFile); err == nil {
		var st enrollState
		if json.Unmarshal(raw, &st) == nil && st.Nonce != "" && st.Token != "" {
			return st
		}
	}
	st := enrollState{Nonce: randB64URL(32), Token: randB64URL(32)}
	if err := writeEnrollState(st); err != nil {
		logln("matricula: WARN nao persistiu enroll.state: %v (seguindo em memoria)", err)
	}
	return st
}

// waitForRustDeskID bloqueia ate o ID existir (ou stop). O cliente pode estar subindo.
func waitForRustDeskID(stop <-chan struct{}) (string, bool) {
	for {
		if id := discoverRustdeskID(); id != "" {
			return id, true
		}
		if exe, err := findRustDeskExe(); err == nil {
			if id, err := getRustDeskID(exe); err == nil && id != "" {
				return id, true
			}
		}
		logln("matricula: aguardando o ID desta maquina...")
		select {
		case <-stop:
			return "", false
		case <-time.After(claimPollInterval):
		}
	}
}

func claimHeaders(req *http.Request) {
	req.Header.Set("content-type", "application/json")
	req.Header.Set("apikey", anonKey)
	req.Header.Set("authorization", "Bearer "+anonKey)
}

func postClaimRegister(rid, nonceHash, tokenHash, host, osStr string) {
	payload, _ := json.Marshal(map[string]string{
		"rustdesk_id": rid, "nonce_hash": nonceHash, "agent_token_hash": tokenHash,
		"hostname": host, "os": osStr,
	})
	req, err := http.NewRequest("POST", claimRegisterURL, bytes.NewReader(payload))
	if err != nil {
		logln("claim-register: erro ao montar req: %v", err)
		return
	}
	claimHeaders(req)
	resp, err := httpClient.Do(req)
	if err != nil {
		logln("claim-register FALHOU: %v", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
	logln("claim-register -> HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
}

// postClaimStatus devolve "" em erro de rede; senao
// waiting|approved|consumed|expired|rejected|unknown.
func postClaimStatus(rid, nonce string) string {
	payload, _ := json.Marshal(map[string]string{"rustdesk_id": rid, "nonce": nonce})
	req, err := http.NewRequest("POST", claimStatusURL, bytes.NewReader(payload))
	if err != nil {
		return ""
	}
	claimHeaders(req)
	resp, err := httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var out struct {
		Status string `json:"status"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.Status
}

// runMatricula bloqueia ate a maquina ser adotada. Retorna false se stop veio antes.
func runMatricula(stop <-chan struct{}) bool {
	rid, ok := waitForRustDeskID(stop)
	if !ok {
		return false
	}
	logln("matricula: rustdesk_id = %s", rid)

	st := loadOrCreateState()
	nonceHash := sha256Hex(st.Nonce)
	tokenHash := sha256Hex(st.Token)
	host := machineAlias()
	osStr := osString()

	postClaimRegister(rid, nonceHash, tokenHash, host, osStr)
	logln("matricula: aguardando adocao — tecnico adota pelo ID %s no painel", rid)

	for {
		select {
		case <-stop:
			return false
		case <-time.After(claimPollInterval):
		}

		switch postClaimStatus(rid, st.Nonce) {
		case "approved", "consumed":
			// grava o token CONFIRMADO com ACL (reusa o writer do enroll.go) e limpa o estado
			if err := writeCredentials(st.Token, rid); err != nil {
				logln("matricula: ERRO ao gravar credencial: %v (retentando)", err)
				continue
			}
			_ = os.Remove(enrollStateFile)
			logln("matricula: APROVADO — token gravado, virando modo sessao")
			return true
		case "waiting":
			// segue esperando
		case "expired", "rejected", "unknown":
			logln("matricula: pedido morto/sumido -> re-registrando")
			postClaimRegister(rid, nonceHash, tokenHash, host, osStr)
		default:
			logln("matricula: poll sem resposta util, retentando")
		}
	}
}
