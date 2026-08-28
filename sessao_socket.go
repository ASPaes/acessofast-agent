// AcessoFast — agente: prova de vida da sessao FORA do log do cliente.
//
// O fim de sessao depende de ler "#N Connection closed" no log. Quando essa linha se
// perde (queda abrupta que nao loga nada, rename de log fora do alcance do tail), o #N
// fica preso em t.open, o heartbeat continua de 20 em 20s e o painel mostra "Em
// atendimento" para sempre — o fantasma diagnosticado em 18/08/2026 na DESKTOP-3SL0480.
// O expireStale so soltava isso com 24h.
//
// Aqui olhamos o que o log nao conta: os sockets TCP do processo do cliente branded.
// Sessao (relay ou direta) sempre carrega um socket ESTABLISHED; a maquina ociosa so
// mantem o vinculo com o rendezvous. Sem socket de sessao por semSocketJanela seguidos,
// a sessao acabou de fato — encerramos e giramos a senha efemera, como no fim normal.
//
// DELIBERADAMENTE nao ha regra por duracao: sessao de plano pago nao tem teto de horas.
// O corte por tempo continua sendo so do hard_cap do plano gratuito.

// A LEITURA (PID do cliente e sockets do processo) e por plataforma: no Windows sai do
// SCM + GetExtendedTcpTable, no macOS do launchd + lsof. A REGRA de decisao fica aqui,
// uma so pros dois SOs — e nela que mora o risco caro de encerrar a sessao de alguem
// que ainda esta trabalhando.

package main

import (
	"time"
)

const (
	// Portas do rendezvous do servidor proprio (padrao RustDesk, preservado no build do
	// cliente): 21115 = teste de NAT, 21116 = rendezvous. Sao o vinculo da maquina
	// OCIOSA — nao contam como sessao. Tudo o mais (21117 do relay, porta efemera do
	// peer no acesso direto/furado) so existe com alguem conectado.
	portaNatTest    = 21115
	portaRendezvous = 21116

	// semSocketJanela: quanto tempo sem socket de sessao antes de declarar o fim. Larga
	// de proposito — encerrar a sessao de alguem que ainda esta trabalhando e o erro
	// caro, e um fantasma vive de qualquer jeito ate a maquina desligar. Cobre reconexao
	// de relay, blip de rede e o proprio graceWindow.
	semSocketJanela = 5 * time.Minute
)

// checkSessaoViva encerra a sessao quando ha #N aberto mas nenhum socket de sessao por
// semSocketJanela seguidos. Chamada a cada tick do poll, na goroutine do worker.
//
// Qualquer leitura duvidosa (servico parado, SCM ou iphlpapi falhando) REARMA o relogio
// em vez de acusar — na duvida o fantasma sobrevive, que e o custo barato. Fim declarado
// aqui vale como fim real: esvazia o conjunto, manda "end" e gira a senha efemera (sem
// isso a senha da sessao fantasma seguia valida, que era o furo de seguranca do caso).
// Nao reinicia o cliente: nao ha o que derrubar, e um falso positivo nao pode cortar
// ninguem.
func (t *tailer) checkSessaoViva() {
	if len(t.open) == 0 {
		t.semSocketDesde = time.Time{}
		return
	}
	pid, ok := clientProcPID()
	if !ok {
		t.semSocketDesde = time.Time{}
		return
	}
	n, ok := socketsDeSessao(pid)
	if !ok || n > 0 {
		t.semSocketDesde = time.Time{}
		return
	}
	if t.semSocketDesde.IsZero() {
		t.semSocketDesde = time.Now()
		return
	}
	if time.Since(t.semSocketDesde) < semSocketJanela {
		return
	}

	logln("<<< %d conexao(oes) presa(s) sem socket de sessao ha %s — SESSAO ENCERRADA (fantasma)",
		len(t.open), semSocketJanela)
	// Antes de esvaziar t.open: este fim nao passa pelo "closed", entao e aqui que uma
	// conexao presa ha mais que o limiar se declara sessao de verdade. Chegar ate aqui
	// ja exige semSocketJanela (5 min) de #N aberto, entao na pratica sempre marca.
	t.autenticaPorTempo()
	t.semSocketDesde = time.Time{}
	t.open = make(map[string]time.Time)
	t.graceUntil = time.Time{}
	t.hardCapUntil = time.Time{}
	t.controllerID = ""
	postEvent("end", "")
	t.rotacionarSeAutenticada("fantasma")
}
