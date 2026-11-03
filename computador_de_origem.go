package main

import "sync/atomic"

// De onde veio o acesso, em toda sessao (03/11/2026).
//
// A Auditoria mostra o computador de origem, e o log de seguranca da empresa depende disso.
// Mas o agente so informava uma vez por sessao DELE, e no servidor a sessao pode mudar sem o
// agente saber: um novo clique em Conectar abre outra ficha, e o heartbeat de uma ficha ja
// fechada abre uma sessao nova. Essas ficavam sem origem (26 de 134 na ASP, em 30 dias).
//
// Toda resposta de start e heartbeat traz o id da sessao. Quando ele muda, o agente informa a
// origem de novo. Nao precisa de mudanca no servidor, e o custo e um POST por sessao nova.

// sessaoNoServidor guarda o id da ultima sessao que o servidor devolveu ("" = nenhuma).
var sessaoNoServidor atomic.Value

func marcaSessaoNoServidor(id string) { sessaoNoServidor.Store(id) }

func leSessaoNoServidor() string {
	s, _ := sessaoNoServidor.Load().(string)
	return s
}

// postaOrigem manda o 'start' com o controlador e anota em qual sessao ele entrou. A marca e
// zerada antes de propósito: se o POST falhar, ela fica vazia, o proximo heartbeat a preenche
// e informaOrigem tenta de novo.
func (t *tailer) postaOrigem() {
	marcaSessaoNoServidor("")
	if hc := postEvent("start", t.controllerID); !hc.IsZero() {
		t.hardCapUntil = hc
	}
	if s := leSessaoNoServidor(); s != "" {
		t.origemInformadaEm = s
	}
}

// informaOrigem roda depois de cada heartbeat: se o servidor esta numa sessao que ainda nao
// recebeu a origem, informa agora.
func (t *tailer) informaOrigem() {
	if t.controllerID == "" {
		return
	}
	atual := leSessaoNoServidor()
	if atual == "" || atual == t.origemInformadaEm {
		return
	}
	logln(">>> sessao nova no servidor (%s) — informando o controlador %s", atual, t.controllerID)
	t.postaOrigem()
}
