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
	claimRegisterURL = "https://plmfyibyrowbgjjyblcl.supabase.co/functions/v1/claim-register"
	claimStatusURL   = "https://plmfyibyrowbgjjyblcl.supabase.co/functions/v1/claim-status"
	enrollStateFile  = baseDir + `\enroll.state` // {nonce, token, started_at} persistido durante a matricula

	// Cadencia do poll em DUAS velocidades. Antes era 15s fixo pra sempre, e o
	// "pra sempre" era literal: uma maquina instalada e nunca adotada seguia
	// perguntando 5.760 vezes por dia, indefinidamente. Em 02/09/2026 isso era
	// 146 mil chamadas/dia — 56% de TODAS as invocacoes de edge function do
	// projeto, vindas de 61 maquinas que ninguem nunca adotou.
	//
	// A pressa so vale no comeco: uma maquina recem-instalada pode ser aprovada a
	// qualquer segundo, e ai os 15s importam. Uma que espera ha dias nao vai ser
	// aprovada nos proximos minutos. Entao a janela quente fica IDENTICA ao que
	// era (nada muda na instalacao normal, que e o que o operador sente) e so
	// depois dela o agente desacelera.
	claimPollHot   = 15 * time.Second // <= claimHotWindow: mesma experiencia de sempre
	claimPollCold  = 10 * time.Minute // depois disso: o pedido claramente ficou esquecido
	claimHotWindow = 2 * time.Hour

	// Sondagem LOCAL do RustDesk ID enquanto o cliente sobe. Compartilhava a
	// constante do poll por coincidencia de valor, mas nao tem nada a ver: nao
	// fala com o servidor, e esticar isto atrasaria a partida do agente.
	ridProbeInterval = 15 * time.Second
)

// enrollState e persistido em disco (ACL restrita) para sobreviver a restart:
// mesmo nonce+token -> mesmo pedido -> o hash adotado bate com o token final.
type enrollState struct {
	Nonce string `json:"nonce"` // prova de posse (nunca sai da maquina, exceto no poll TLS)
	Token string `json:"token"` // vira agent.token na adocao; so o hash e registrado
	// Instante em que ESTA matricula comecou, ancora da cadencia do poll. Vive no
	// disco de proposito: em memoria, um restart do servico devolveria a maquina
	// pra janela quente, e a matricula re-registra o claim de hora em hora (o
	// claim expira), o que zeraria um contador ancorado no pedido atual. Nos dois
	// casos o poll de 15s voltaria pra sempre — exatamente o que estamos tirando.
	// omitempty: enroll.state ja gravado por versao anterior nao tem o campo.
	StartedAt time.Time `json:"started_at,omitempty"`
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
			// Estado gravado por versao anterior nao tem started_at. Carimba AGORA e
			// persiste: a maquina ganha uma ultima janela quente (uma vez so, ~480
			// chamadas) e depois entra na cadencia lenta. O caminho oposto — tratar
			// zero como "antiquissimo" — jogaria direto pro poll de 10 min uma
			// matricula que talvez tenha comecado ha 5 minutos, atrasando uma adocao
			// legitima em curso. Errar pro lado do operador.
			if st.StartedAt.IsZero() {
				st.StartedAt = time.Now()
				if err := writeEnrollState(st); err != nil {
					logln("matricula: WARN nao persistiu started_at: %v (janela quente por sessao)", err)
				}
			}
			return st
		}
	}
	st := enrollState{Nonce: randB64URL(32), Token: randB64URL(32), StartedAt: time.Now()}
	if err := writeEnrollState(st); err != nil {
		logln("matricula: WARN nao persistiu enroll.state: %v (seguindo em memoria)", err)
	}
	return st
}

// claimPollDelay: 15s enquanto a matricula e recente, 10 min depois disso.
//
// StartedAt zerado (o writeEnrollState falhou e nao ha disco onde ancorar) cai na
// janela quente: sem ancora confiavel, o certo e nao atrasar uma adocao real.
func claimPollDelay(startedAt time.Time) time.Duration {
	if startedAt.IsZero() || time.Since(startedAt) < claimHotWindow {
		return claimPollHot
	}
	return claimPollCold
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
		case <-time.After(ridProbeInterval):
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

// startMatricula NAO bloqueia: descobre o rustdesk_id, seta o token PENDENTE (+
// rustdeskID) em memoria, registra o claim e sobe a goroutine que finaliza a
// credencial quando a maquina for adotada. Retorna false so se stop veio antes de
// o ID existir.
//
// Chave da auto-adocao: o token pendente (st.Token) ja tem o hash registrado no
// claim, entao o agente ja pode postar eventos de sessao ANTES de ser adotado. Num
// acesso direto, o tailer posta 'start' com controller_rustdesk_id e o servidor
// (auto_adopt_direct) adota o device pelo hash do token, consumindo o claim. Sem
// isto havia deadlock: o agente esperava o claim ser consumido pra ligar o tailer,
// e o claim so seria consumido por um 'start' que so o tailer mandaria.
func startMatricula(stop <-chan struct{}) bool {
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

	// Token PENDENTE em memoria: habilita postEvent (e a auto-adocao) desde ja. So
	// vira credencial persistida (agent.token) quando o claim for consumido.
	token = st.Token
	rustdeskID = rid

	postClaimRegister(rid, nonceHash, tokenHash, host, osStr)
	logln("matricula: claim registrado; tailer ativo — adocao pelo painel OU acesso direto")

	go finalizeMatricula(stop, rid, st, nonceHash, tokenHash, host, osStr)
	return true
}

// finalizeMatricula acompanha o claim-status e, quando a maquina for adotada
// (manual no painel OU auto-adocao pelo acesso direto), persiste a credencial. O
// token persistido e o MESMO ja usado nos eventos de sessao (st.Token), entao a
// transicao e transparente pro tailer, que segue rodando sem interrupcao.
func finalizeMatricula(stop <-chan struct{}, rid string, st enrollState, nonceHash, tokenHash, host, osStr string) {
	esfriou := false
	for {
		delay := claimPollDelay(st.StartedAt)
		if delay == claimPollCold && !esfriou {
			esfriou = true
			logln("matricula: sem adocao ha %s — poll passa a %s (a adocao ainda funciona, so demora ate esse tempo pra ser notada)",
				claimHotWindow, claimPollCold)
		}
		select {
		case <-stop:
			return
		case <-time.After(delay):
		}

		switch postClaimStatus(rid, st.Nonce) {
		case "approved", "consumed":
			// grava o token CONFIRMADO com ACL (reusa o writer do enroll.go) e limpa o estado
			if err := writeCredentials(st.Token, rid); err != nil {
				logln("matricula: ERRO ao gravar credencial: %v (retentando)", err)
				continue
			}
			_ = os.Remove(enrollStateFile)
			logln("matricula: ADOTADO — credencial persistida; matricula concluida")
			// A adocao acabou de criar o device no painel. So AGORA o reporte da senha
			// e aceito (antes disso levava 404), e o painel esta SEM senha nenhuma: a
			// adocao nao provisiona mais senha justamente pra nao servir uma senha que
			// nunca esteve nesta maquina. Publica na hora — sem isto o tecnico esperaria
			// o tick do rotateRetryLoop pra conseguir o primeiro acesso.
			go publishSecretAfterAdoption()
			return
		case "waiting":
			// segue esperando (o tailer ja esta ativo com o token pendente)
		case "expired", "rejected", "unknown":
			logln("matricula: pedido morto/sumido -> re-registrando")
			postClaimRegister(rid, nonceHash, tokenHash, host, osStr)
		default:
			logln("matricula: poll sem resposta util, retentando")
		}
	}
}
