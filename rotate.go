// AcessoFast — rotate.go — ROTACAO DA SENHA EFEMERA (Fase 2).
//
// Ao fim de CADA sessao, a senha permanente do endpoint e trocada: a senha que o
// tecnico viu naquela sessao morre aqui. Modelo (decidido): o AGENTE gera, aplica
// no RustDesk e SO ENTAO reporta ao painel a senha que aplicou com sucesso.
//
// INVARIANTE: o painel nunca conhece uma senha que ainda nao esta no endpoint.
//  1. gera senha nova (mesma politica do provision-device-secret)
//  2. aplica no cliente branded:  AcessoFast.exe --password <nova>
//  3. reporta ao painel (rotate-device-secret); em falha, PERSISTE a pendencia
//     (ACL restrita) e um loop de retry reenvia ate o painel confirmar (HTTP 200).
//
// Enquanto a pendencia nao confirma, o painel ainda serve a senha ANTIGA -> o
// proximo tecnico pode nao conectar por alguns segundos. E auto-recuperavel e o
// pior caso e "espera", nunca "acesso vazado". Por isso o retry e curto.
//
// Reusa de main.go/enroll.go (package main, fonte unica): baseDir, token,
// rustdeskID, anonKey, httpClient, logln, readTrim, hardenDir, findRustDeskExe.
package main

import (
	"bytes"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	rotateURL         = "https://plmfyibyrowbgjjyblcl.supabase.co/functions/v1/rotate-device-secret"
	pendingFile       = baseDir + `\rotate.pending` // senha JA aplicada no endpoint aguardando confirmacao do painel
	rotateRetryPeriod = 30 * time.Second
)

// Politica de senha — ESPELHA provision-device-secret (alfabeto sem ambiguos 0 O 1 l I).
const (
	pwLower  = "abcdefghijkmnpqrstuvwxyz" // sem l o
	pwUpper  = "ABCDEFGHJKLMNPQRSTUVWXYZ" // sem I O
	pwDigits = "23456789"                 // sem 0 1
	pwLen    = 20
)

var pwAlphabet = pwLower + pwUpper + pwDigits

// rotateMu serializa rotacao e retry: dois "end" seguidos nao rodam concorrentes,
// e o estado (pendencia + senha do endpoint) fica consistente sob o lock.
var rotateMu sync.Mutex

// pwRandIndex: indice uniforme em [0,n) via rejection sampling de 1 byte -> sem vies de modulo.
func pwRandIndex(n int) int {
	if n <= 0 {
		return 0
	}
	max := 256 - (256 % n)
	var b [1]byte
	for {
		_, _ = crand.Read(b[:])
		if int(b[0]) < max {
			return int(b[0]) % n
		}
	}
}

func pwPick(pool string) byte { return pool[pwRandIndex(len(pool))] }

// genPassword: 1 de cada classe exigida (minuscula, maiuscula, digito) + preenche + embaralha (Fisher-Yates).
func genPassword() string {
	chars := []byte{pwPick(pwLower), pwPick(pwUpper), pwPick(pwDigits)}
	for len(chars) < pwLen {
		chars = append(chars, pwPick(pwAlphabet))
	}
	for i := len(chars) - 1; i > 0; i-- {
		j := pwRandIndex(i + 1)
		chars[i], chars[j] = chars[j], chars[i]
	}
	return string(chars)
}

// applyPassword seta a senha permanente no cliente branded. ASSUNCAO (validar em
// maquina real): AcessoFast.exe --password <pw> persiste a senha permanente. O
// agente ja usa AcessoFast.exe --get-id no enroll, entao o CLI existe.
func applyPassword(exe, pw string) error {
	out, err := exec.Command(exe, "--password", pw).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// reportRotation POSTa a senha nova ao painel. true SO em HTTP 200.
func reportRotation(pw string) bool {
	payload, _ := json.Marshal(map[string]string{
		"rustdesk_id": rustdeskID, "agent_token": token, "password": pw,
	})
	req, err := http.NewRequest("POST", rotateURL, bytes.NewReader(payload))
	if err != nil {
		logln("ROTATE report erro ao montar req: %v", err)
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", anonKey)
	req.Header.Set("Authorization", "Bearer "+anonKey)
	resp, err := httpClient.Do(req)
	if err != nil {
		logln("ROTATE report FALHOU: %v", err)
		return false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
	if resp.StatusCode == 200 {
		return true
	}
	logln("ROTATE report -> HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	return false
}

func writePending(pw string) error {
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return err
	}
	_ = hardenDir(baseDir) // SYSTEM + Admins (mesma ACL do agent.token/enroll.state)
	if err := os.WriteFile(pendingFile, []byte(pw), 0o600); err != nil {
		return err
	}
	_ = hardenDir(baseDir)
	return nil
}

func readPending() string { return readTrim(pendingFile) }
func clearPending()       { _ = os.Remove(pendingFile) }

// rotateNow gira a senha ao fim de uma sessao. Chamar em goroutine: faz exec + HTTP
// e nao pode bloquear o poll de deteccao de sessao.
func rotateNow() {
	rotateMu.Lock()
	defer rotateMu.Unlock()

	if token == "" || rustdeskID == "" {
		logln("ROTATE skip: token/rustdesk_id ausente (matricula pendente?)")
		return
	}
	exe, err := findRustDeskExe()
	if err != nil {
		logln("ROTATE ABORT: cliente branded nao encontrado: %v", err)
		return
	}

	pw := genPassword()

	// 1) aplica ANTES de reportar. Se falhar, mantem a senha antiga nos DOIS lados
	//    (consistente, sem lockout) e tenta de novo na proxima sessao.
	if err := applyPassword(exe, pw); err != nil {
		logln("ROTATE ABORT: --password falhou: %v (senha antiga mantida)", err)
		return
	}

	// 2) a senha nova JA esta no endpoint -> registra a pendencia antes de reportar,
	//    pra sobreviver a crash/queda de rede entre aplicar e confirmar.
	if err := writePending(pw); err != nil {
		logln("ROTATE WARN: nao persistiu pendencia: %v (seguindo com envio em memoria)", err)
	}

	// 3) reporta; sucesso -> limpa a pendencia. Falha -> o retry loop reenvia.
	if reportRotation(pw) {
		clearPending()
		logln("ROTATE ok: senha rotacionada e confirmada pelo painel")
	} else {
		logln("ROTATE pendente: aplicada no endpoint, painel ainda nao confirmou (retry em background)")
	}
}

// rotateRetryLoop reenvia a senha pendente ate o painel confirmar. Roda ate stop.
// Tambem cobre a pendencia deixada por um run anterior que caiu antes de confirmar.
func rotateRetryLoop(stop <-chan struct{}) {
	flush := func() {
		if readPending() == "" || token == "" || rustdeskID == "" {
			return
		}
		rotateMu.Lock()
		defer rotateMu.Unlock()
		pw := readPending() // re-le sob lock: rotateNow pode ter limpado/atualizado
		if pw != "" && reportRotation(pw) {
			clearPending()
			logln("ROTATE pendencia confirmada pelo painel")
		}
	}

	flush() // tenta imediatamente ao subir
	t := time.NewTicker(rotateRetryPeriod)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			flush()
		}
	}
}
