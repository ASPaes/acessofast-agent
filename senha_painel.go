// AcessoFast — senha_painel.go — SENHA DEFINIDA PELO PAINEL (Passo 2 do plano
// "Aposentar a Senha Rotativa").
//
// PARA QUE SERVE
// Com a rotacao de rotina desligada (Passo 1, modo install_only), a senha de uma maquina
// fica parada. Este arquivo e o jeito de TROCA-LA sem ir ate a maquina: alguem define a
// senha no painel, o servidor guarda como pedido e entrega no 'presence', e o agente
// aplica aqui.
//
// A INVARIANTE DA FASE 2 CONTINUA VALENDO
// O painel nunca conhece uma senha que ainda nao esta no endpoint. Por isso o pedido
// NAO e a senha do painel: o Conectar segue entregando a antiga ate o agente confirmar.
// A confirmacao e o mesmo caminho da rotacao — aplica com --password, grava o
// rotate.pending, reporta ao rotate-device-secret — com o pedido_id junto. Se o reporte
// falhar, o rotateRetryLoop reenvia com o pedido_id (que fica em senha.pedido ao lado
// da pendencia) ate o painel confirmar.
//
// O QUE PODE DAR ERRADO, E O QUE ACONTECE
//   - cliente parado ou --password falhando: nada e aplicado, nada e reportado. O pedido
//     continua no servidor e volta no proximo presence (3 min).
//   - resposta perdida depois de aplicar: o retry reenvia. Se o presence trouxer o mesmo
//     pedido antes disso, pedidoJaAplicado evita aplicar de novo.
//   - rotacao no meio (corte do hard_cap): rotateNow sorteia outra senha e APAGA o
//     senha.pedido, entao a pendencia dela nunca e confirmada como se fosse o pedido. O
//     pedido, que seguiu no servidor, e reaplicado no presence seguinte.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// senhaDoPainel: bloco "senha" da resposta do presence.
type senhaDoPainel struct {
	PedidoID string `json:"pedido_id"`
	Senha    string `json:"senha"`
}

// pedidoFile: o pedido_id da senha que esta em rotate.pending. Var, e nao const, para
// os testes usarem um diretorio temporario. Nao e segredo (o segredo e a pendencia, que
// ja passa pelo hardenDir): um pedido_id sozinho nao abre nada.
var pedidoFile = baseDir + `\senha.pedido`

// Regra da senha — ESPELHA a edge definir-senha-dispositivo (senhaAceitavel). O agente
// confere de novo porque a senha vai para uma linha de comando: nunca aplicar cego o
// que veio da rede.
const (
	senhaMin = 8
	senhaMax = 64
)

var (
	senhaRe    = regexp.MustCompile(`^[A-Za-z0-9!@#$%*\-_=+.?]+$`)
	letraRe    = regexp.MustCompile(`[A-Za-z]`)
	digitoRe   = regexp.MustCompile(`[0-9]`)
	pedidoIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

func senhaAceitavel(pw string) bool {
	return len(pw) >= senhaMin && len(pw) <= senhaMax &&
		senhaRe.MatchString(pw) && letraRe.MatchString(pw) && digitoRe.MatchString(pw)
}

// pedidoJaAplicado: a senha deste pedido ja esta na maquina e so falta o painel
// confirmar — o retry cuida disso, e aplicar de novo so repetiria o --password.
func pedidoJaAplicado(pendente, pedidoGravado, pedidoID string) bool {
	return pendente != "" && pedidoGravado != "" && pedidoGravado == pedidoID
}

func readPedido() string { return readTrim(pedidoFile) }
func clearPedido()       { _ = os.Remove(pedidoFile) }

func writePedido(id string) error {
	if err := os.MkdirAll(filepath.Dir(pedidoFile), 0o700); err != nil {
		return err
	}
	return os.WriteFile(pedidoFile, []byte(id), 0o600)
}

// aplicaSenhaDoPainel aplica a senha pedida e confirma ao painel. Roda inline no ramo de
// presence (maquina ociosa), antes do auto-update: se os dois vierem juntos, a senha e
// aplicada e reportada antes de o servico reiniciar.
//
// Fora do modo de rotacao de proposito: nao e rotina, e alguem pediu.
func aplicaSenhaDoPainel(s *senhaDoPainel) {
	if s == nil {
		return
	}
	// A senha NUNCA vai para o log — nem aqui, nem no erro do --password.
	if !pedidoIDRe.MatchString(s.PedidoID) || !senhaAceitavel(s.Senha) {
		logln("SENHA DO PAINEL recusada: pedido %q fora do formato esperado", s.PedidoID)
		return
	}

	rotateMu.Lock()
	defer rotateMu.Unlock()

	if pedidoJaAplicado(readPending(), readPedido(), s.PedidoID) {
		return
	}
	if token == "" || rustdeskID == "" {
		return
	}
	exe, err := findRustDeskExe()
	if err != nil {
		logln("SENHA DO PAINEL adiada: cliente branded nao encontrado: %v", err)
		return
	}
	// Mesmo GATE do rotateNow: com o servico do cliente parado o --password pode sair
	// com 0 sem ninguem receber, e a confirmacao poria no painel uma senha que a maquina
	// nao tem (caso BOMBONIERI03).
	if !clienteVivo() {
		logln("SENHA DO PAINEL adiada: servico do cliente parado (o pedido volta no proximo presence)")
		return
	}
	if err := applyPassword(exe, s.Senha); err != nil {
		logln("SENHA DO PAINEL adiada: --password falhou: %v (o pedido volta no proximo presence)", err)
		return
	}

	// Aplicada: a partir daqui a senha so existe na maquina ate o painel confirmar.
	// Pendencia primeiro, pedido depois — um crash entre os dois confirma a senha sem
	// pedido_id, o que ainda deixa o painel certo (o pedido so e reaplicado depois).
	if err := writePending(s.Senha); err != nil {
		logln("SENHA DO PAINEL WARN: nao persistiu pendencia: %v", err)
	}
	if err := writePedido(s.PedidoID); err != nil {
		logln("SENHA DO PAINEL WARN: nao persistiu o pedido: %v", err)
	}

	if reportRotation(s.Senha, s.PedidoID) {
		clearPending()
		logln("SENHA DO PAINEL aplicada e confirmada (pedido %s)", s.PedidoID)
	} else {
		logln("SENHA DO PAINEL aplicada; painel ainda nao confirmou o pedido %s (retry em background)", s.PedidoID)
	}
}

// corpoParaLog prepara a resposta da session-ingest para o agent.log: omite o bloco
// "senha" e trunca. O postEventFull loga toda resposta, e sem isto a senha pedida
// ficaria em texto claro num arquivo da maquina do cliente.
func corpoParaLog(body []byte) string {
	s := strings.TrimSpace(string(body))
	if strings.Contains(s, `"senha"`) {
		var m map[string]json.RawMessage
		if json.Unmarshal(body, &m) != nil {
			// Ilegivel e com cara de ter senha: nao arrisca.
			s = "[resposta omitida: contem senha]"
		} else if _, ok := m["senha"]; ok {
			m["senha"] = json.RawMessage(`"[omitida]"`)
			if b, err := json.Marshal(m); err == nil {
				s = string(b)
			} else {
				s = "[resposta omitida: contem senha]"
			}
		}
	}
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}
