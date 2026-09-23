// AcessoFast — agente: prova de vida da sessao FORA do log do cliente.
//
// O fim de sessao depende de ler "#N Connection closed" no log. Quando essa linha se
// perde (queda abrupta que nao loga nada, rename de log fora do alcance do tail), o #N
// fica preso em t.open, o heartbeat continua de 20 em 20s e o painel mostra "Em
// atendimento" para sempre — o fantasma diagnosticado em 18/08/2026 na DESKTOP-3SL0480.
// O expireStale so soltava isso com 24h.
//
// Aqui olhamos o que o log nao conta: os sockets TCP do cliente branded — do servico E
// DOS PROCESSOS FILHOS DELE (ver pidsDoCliente; olhar so o servico foi o bug de 18/09).
// Sessao (relay ou direta) sempre carrega um socket ESTABLISHED; a maquina ociosa so
// mantem o vinculo com o rendezvous. Sem socket de sessao por semSocketJanela seguidos,
// a sessao acabou de fato — encerramos e giramos a senha efemera, como no fim normal.
//
// ATENCAO ao aposentar a senha rotativa: o fim declarado aqui passa por
// rotacionarSeAutenticada, e girar a senha nao e enfeite — era o furo do caso original,
// a senha da sessao fantasma seguia valida depois de o servidor encerrar a sessao.
// Desligar a rotacao sem decidir o que invalida essa senha reabre o furo.
//
// DELIBERADAMENTE nao ha regra por duracao: sessao de plano pago nao tem teto de horas.
// O corte por tempo continua sendo so do hard_cap do plano gratuito.

// A LEITURA do sistema e por plataforma: clientProcPID, mapaDePais e socketsDeSessao
// vivem em plat_windows.go e plat_darwin.go. Aqui ficam a REGRA de decisao e a
// caminhada na arvore de processos — que e onde da para errar, e por isso e testavel
// sem sistema nenhum.

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

// ---- Arvore de processos do cliente ----------------------------------------------
//
// POR QUE ISTO EXISTE (corrigido em 18/09/2026).
//
// A primeira versao contava os sockets do PID que o SCM devolve para o servico
// `AcessoFast` — o processo `AcessoFast.exe --service`. Esse processo NAO SEGURA
// SOCKET NENHUM: ele so supervisiona e gera um filho, e e o filho que fica com a
// conexao da sessao.
//
// Entao socketsDeSessao devolvia 0 com o tecnico conectado, e aos 5 minutos o
// checkSessaoViva declarava fantasma e encerrava a sessao viva. Medido no banco:
// duracao entre 295 e 325 segundos virou a MEDIANA da frota a partir do rollout do
// build que trouxe este arquivo — zero ocorrencias em ~230 sessoes antes dele.
//
// Prova direta na maquina de teste: servico PID 3776 (`--service`, pai 1216) com ZERO
// conexoes TCP em qualquer estado; filho PID 15144 com ParentProcessID=3776 segurando
// os sockets. O rendezvous 21116 nem aparece em TCP (e UDP), o que mostra que a
// exclusao de 21115/21116 tambem tinha sido escrita pensando no processo errado — ela
// fica, porque nao custa nada e cobre o caso em que o vinculo seja TCP.

// descendentesDe devolve a raiz mais tudo que descende dela, a partir do mapa
// filho -> pai. Funcao PURA de proposito: a leitura do Windows fica em mapaDePais, e
// a regra de caminhada — que e onde da para errar — fica testavel sem sistema.
//
// Tolera CICLO. ParentProcessID e um numero cru do snapshot: se o pai morreu e o PID
// foi reciclado, o mapa pode formar laco (A->B->A). Sem o conjunto de visitados isso
// seria travamento do worker, nao contagem errada.
func descendentesDe(pais map[uint32]uint32, raiz uint32) map[uint32]bool {
	filhos := make(map[uint32][]uint32, len(pais))
	for filho, pai := range pais {
		if filho == pai {
			continue // processo pai de si mesmo: lixo do snapshot, ignora
		}
		filhos[pai] = append(filhos[pai], filho)
	}
	vistos := map[uint32]bool{raiz: true}
	fila := []uint32{raiz}
	for len(fila) > 0 {
		atual := fila[0]
		fila = fila[1:]
		for _, f := range filhos[atual] {
			if vistos[f] {
				continue
			}
			vistos[f] = true
			fila = append(fila, f)
		}
	}
	return vistos
}

// pidsDoCliente: o PID do servico do cliente branded MAIS todos os descendentes dele.
//
// NAO filtra por nome do executavel de proposito. Um vinculo de parentesco errado (PID
// reciclado) faria contar socket de um processo alheio, e o efeito disso e um fantasma
// durar mais — o erro barato. Filtrar por nome arriscaria o contrario: um dia o cliente
// passa a entregar a conexao a um auxiliar com outro nome, a contagem zera e voltamos a
// cortar sessao viva, que e o erro caro. A mesma escolha do resto do arquivo.
func pidsDoCliente() (map[uint32]bool, bool) {
	raiz, ok := clientProcPID()
	if !ok {
		return nil, false
	}
	pais, ok := mapaDePais()
	if !ok {
		return nil, false
	}
	return descendentesDe(pais, raiz), true
}

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
	pids, ok := pidsDoCliente()
	if !ok {
		t.semSocketDesde = time.Time{}
		return
	}
	n, ok := socketsDeSessao(pids)
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
