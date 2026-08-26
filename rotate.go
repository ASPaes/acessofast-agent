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

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	rotateURL   = "https://plmfyibyrowbgjjyblcl.supabase.co/functions/v1/rotate-device-secret"
	pendingFile = baseDir + `\rotate.pending` // senha JA aplicada no endpoint aguardando confirmacao do painel

	// Cadencia do reenvio da pendencia, escalonada pelo tempo que ela esta aberta.
	// O caso comum e a INSTALACAO NOVA: o rotate-on-boot aplica a senha antes de a
	// maquina ser adotada, o painel recusa o reporte (404 device_not_registered) e a
	// pendencia fica aberta ate a adocao. Nesse intervalo o painel nao tem senha
	// nenhuma e o tecnico ve "aguardando" na tela — por isso o inicio e curto (5s;
	// antes era 30s fixo, e o tecnico ficava abrindo e fechando a tela).
	// Depois afrouxa: uma maquina instalada e nunca adotada nao pode ficar batendo
	// na edge function a cada 5s pra sempre.
	rotateRetryFast    = 5 * time.Second
	rotateRetrySlow    = 30 * time.Second
	rotateRetryIdle    = 5 * time.Minute
	rotateRetryFastFor = 2 * time.Minute
	rotateRetrySlowFor = 10 * time.Minute

	// Espera pelo servico do cliente antes de rotacionar no boot (ver esperaCliente).
	// O limite e generoso porque a maquina no boot esta disputando disco com tudo; se
	// estourar, nao rotacionamos — o restart do cliente re-sincroniza depois.
	esperaClienteTick   = 3 * time.Second
	esperaClienteLimite = 5 * time.Minute

	// holdFile (§4.3): "hold de manutencao". Timestamp RFC3339; enquanto now<hold_until,
	// o rotate-on-boot (§3.2) e suprimido (reboot planejado — a sessao pode voltar
	// sozinha). Ausente/ilegivel/expirado = sem hold. O caminho painel->agente que
	// ESCREVE este arquivo e trabalho do §4.3; aqui o agente apenas LE.
	holdFile = baseDir + `\hold.until`
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

// clienteVivo: o servico do cliente branded esta rodando? clientProcPID e por
// plataforma (SCM no Windows, launchd no macOS) e ja devolve false para leitura
// duvidosa — que aqui tratamos como "nao rotaciona", o lado seguro.
func clienteVivo() bool {
	_, ok := clientProcPID()
	return ok
}

// esperaCliente aguarda o servico do cliente subir, ate o limite. Existe por causa do
// BOOT: agente e cliente sao servicos independentes e sobem em paralelo, entao o
// rotate-on-boot quase sempre chegava primeiro e aplicava a senha no vazio. Esperar
// troca uma rotacao imediata (que nao funcionava) por uma que funciona.
func esperaCliente(limite time.Duration) bool {
	fim := time.Now().Add(limite)
	for {
		if clienteVivo() {
			return true
		}
		if !time.Now().Before(fim) {
			return false
		}
		time.Sleep(esperaClienteTick)
	}
}

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

	// GATE (25/08/2026) — sem o servico do cliente RODANDO nao se rotaciona.
	//
	// O --password entrega a senha ao servico do cliente. Com o servico parado nao ha
	// quem receba, mas o comando ainda pode sair com codigo 0: o agente conclui que
	// aplicou, reporta ao painel, e o painel passa a servir uma senha que a maquina
	// NUNCA teve. Quando o servico sobe, ele carrega a senha ANTIGA do disco e todo
	// acesso vira "Senha incorreta" — divergencia permanente ate alguem intervir.
	//
	// Foi o caso BOMBONIERI03 (25/08/2026): servico parado, rotate-on-boot as 16:44
	// aplicou no vazio, e a maquina ficou inacessivel pelo painel por quase 2h.
	//
	// Abortar aqui mantem a senha antiga nos DOIS lados — consistente, sem lockout.
	// Leitura duvidosa (SCM falhando) cai no mesmo ramo de proposito: na duvida, NAO
	// rotaciona. O custo e uma senha que sobrevive mais um ciclo; o custo do contrario
	// e a maquina inacessivel.
	if !clienteVivo() {
		logln("ROTATE ABORT: servico do cliente parado — senha antiga mantida nos dois lados")
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

// holdActive: true se ha um "hold de manutencao" vigente (§4.3) — o operador sinalizou
// um reboot planejado e nao queremos rotacionar no boot (a sessao pode voltar sozinha
// dentro da janela). O hold e um timestamp RFC3339 em holdFile. Ausente/ilegivel/
// expirado => sem hold (fail-secure: na duvida, rotaciona).
func holdActive() bool {
	v := readTrim(holdFile)
	if v == "" {
		return false
	}
	until, err := time.Parse(time.RFC3339, v)
	if err != nil {
		logln("HOLD ignorado: %s ilegivel (%q): %v", holdFile, v, err)
		return false
	}
	if time.Now().Before(until) {
		logln("HOLD ativo ate %s — rotate-on-boot suprimido", until.Format(time.RFC3339))
		return true
	}
	return false
}

// rotateOnBoot implementa §3.2: rotaciona 1x no startup do agente, exceto sob hold.
// Chamada em goroutine pelo worker() (faz exec + HTTP). rotateNow ja e idempotente
// quanto a token/rustdesk_id ausentes e serializa em rotateMu com o retry loop.
func rotateOnBoot() {
	if holdActive() {
		return
	}
	// ESPERA o cliente subir antes de rotacionar. Agente e cliente sao servicos
	// independentes e no boot sobem em paralelo: sem esta espera o agente ganhava a
	// corrida, aplicava a senha sem ninguem para receber e o painel ficava servindo
	// uma senha que a maquina nunca teve (ver o GATE em rotateNow).
	if !esperaCliente(esperaClienteLimite) {
		logln("ROTATE boot: cliente nao subiu em %s — rotacao adiada (o restart do cliente re-sincroniza)", esperaClienteLimite)
		return
	}
	logln("ROTATE boot: rotacionando senha no startup (fecha 'senha sobrevive ao reboot')")
	rotateNow()
}

// rotateAposRestartDoCliente re-sincroniza a senha quando o servico do cliente
// REINICIA (detectClientRestart, em main.go). Ao subir, o cliente recarrega a config
// do disco: a senha que ele passa a exigir e a que estava gravada la, que pode nao ser
// a que o painel conhece. Rotacionar aqui — com o cliente comprovadamente de pe — faz
// os dois lados voltarem a bater sozinhos.
//
// E a rede de seguranca do conjunto: mesmo que uma divergencia apareca por um caminho
// que nao previmos, ela morre no proximo restart do cliente em vez de durar ate alguem
// perceber e intervir na maquina.
func rotateAposRestartDoCliente() {
	logln("ROTATE re-sync: cliente reiniciou e recarregou a config — rotacionando para realinhar com o painel")
	rotateNow()
}

// clientServiceName: o servico Windows do cliente branded (o RustDesk server que
// RECEBE as conexoes). Distinto do agente (serviceName = "AcessoFastAgent" em main.go).
const clientServiceName = "AcessoFast"

// restartClientService reinicia o servico do cliente branded. Billing B2: e o corte
// DURO do free — parar o servico derruba a sessao ativa; ao subir, o cliente recarrega
// a config com a senha JA rotacionada. Roda como SYSTEM (o agente e servico), entao tem
// privilegio pra controlar o SCM. Bounded: espera ate ~20s pelo estado Stopped.
func restartClientService() {
	m, err := mgr.Connect()
	if err != nil {
		logln("CUT: mgr.Connect falhou: %v", err)
		return
	}
	defer m.Disconnect()

	s, err := m.OpenService(clientServiceName)
	if err != nil {
		logln("CUT: OpenService(%s) falhou: %v", clientServiceName, err)
		return
	}
	defer s.Close()

	status, err := s.Control(svc.Stop)
	if err != nil {
		// Ja parado, ou sem controle: loga e ainda tenta subir (idempotente).
		logln("CUT: Stop(%s) falhou: %v (tentando start assim mesmo)", clientServiceName, err)
	} else {
		deadline := time.Now().Add(20 * time.Second)
		for status.State != svc.Stopped && time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			if status, err = s.Query(); err != nil {
				logln("CUT: Query(%s) falhou: %v", clientServiceName, err)
				break
			}
		}
	}

	if err := s.Start(); err != nil {
		logln("CUT: Start(%s) falhou: %v", clientServiceName, err)
		return
	}
	logln("CUT: servico %s reiniciado — sessao derrubada, senha nova ativa", clientServiceName)
}

// flushPending reenvia ao painel a senha pendente (JA aplicada no endpoint).
// true = nao ha mais pendencia: ou nao havia nada a enviar, ou o painel confirmou.
func flushPending() bool {
	if token == "" || rustdeskID == "" {
		return false // sem credencial o painel rejeitaria; nao gasta chamada
	}
	rotateMu.Lock()
	defer rotateMu.Unlock()
	pw := readPending() // le sob lock: rotateNow pode ter limpado/atualizado
	if pw == "" {
		return true
	}
	if reportRotation(pw) {
		clearPending()
		logln("ROTATE pendencia confirmada pelo painel")
		return true
	}
	return false
}

// publishSecretAfterAdoption roda quando a matricula confirma a adocao (matricula.go).
// Ate esse momento o painel RECUSAVA o reporte (device inexistente -> 404), entao a
// senha que o agente aplicou no boot esta so na maquina e o painel nao tem senha
// nenhuma. Publicamos na hora, sem esperar o tick do retry — e o que faz o primeiro
// acesso funcionar de primeira. Sem pendencia (o --password do boot falhou ou o
// rotate-on-boot nem rodou), rotaciona agora: e o unico jeito de o painel passar a
// conhecer a senha desta maquina.
func publishSecretAfterAdoption() {
	if readPending() != "" {
		if !flushPending() {
			logln("ROTATE pos-adocao: painel nao confirmou a pendencia (o retry segue tentando)")
		}
		return
	}
	logln("ROTATE pos-adocao: sem pendencia — rotacionando pra publicar a senha no painel")
	rotateNow()
}

// rotateRetryLoop reenvia a senha pendente ate o painel confirmar. Roda ate stop.
// Tambem cobre a pendencia deixada por um run anterior que caiu antes de confirmar.
// O ticker e curto e a decisao de reenviar sai do intervalo escalonado (ver as
// constantes rotateRetry*): sem pendencia o tick e um stat de arquivo e nada mais.
func rotateRetryLoop(stop <-chan struct{}) {
	var pendenteDesde time.Time   // quando esta pendencia foi vista pela 1a vez
	var ultimaTentativa time.Time // ultimo reporte tentado (zero = nenhum ainda)

	intervalo := func() time.Duration {
		aberta := time.Since(pendenteDesde)
		switch {
		case aberta < rotateRetryFastFor:
			return rotateRetryFast
		case aberta < rotateRetrySlowFor:
			return rotateRetrySlow
		default:
			return rotateRetryIdle
		}
	}

	tick := func() {
		if readPending() == "" {
			pendenteDesde = time.Time{}
			return
		}
		if pendenteDesde.IsZero() {
			pendenteDesde = time.Now()
			ultimaTentativa = time.Time{}
		}
		if !ultimaTentativa.IsZero() && time.Since(ultimaTentativa) < intervalo() {
			return
		}
		ultimaTentativa = time.Now()
		if flushPending() {
			pendenteDesde = time.Time{}
		}
	}

	tick() // tenta imediatamente ao subir
	t := time.NewTicker(rotateRetryFast)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			tick()
		}
	}
}
