// AcessoFast — aviso.go — mostrar um recado NA TELA de quem esta usando a maquina.
//
// PARA QUE SERVE
// Quando o operador acessa, direto pelo cliente, um computador cujo AcessoFast
// esta velho demais para se atualizar sozinho, o painel nao tem como avisar: o
// acesso direto nao passa por tela nossa. E a maquina acessada tambem nao pode
// mostrar nada — quem desenharia a janela la seria o agente dela, justamente o
// desatualizado.
//
// Entao o servidor identifica o operador (a maquina acessada reporta o
// controller_rustdesk_id no 'start') e devolve o recado no presence DELE. Este
// arquivo e o que poe isso na tela.
//
// POR QUE WTSSendMessage, E NAO msg.exe
// A primeira versao usava msg.exe. Testado na maquina do Ryan (Windows 11 Home
// Single Language), ele NAO EXISTE: msg.exe so vem nas edicoes Pro/Server. Como
// Home e o que mais se acha em maquina de operador, o aviso simplesmente nunca
// apareceria — e falharia calado, que e o pior modo de falhar.
//
// WTSSendMessage e a API que o Windows oferece justamente para isto: um servico
// rodando como SYSTEM (Session 0, sem interface) mandar uma caixa de mensagem
// para a sessao INTERATIVA do usuario. Existe em toda edicao, e o agente ja
// importa golang.org/x/sys/windows — nenhuma dependencia nova.
//
// Sem WTSSendMessage a alternativa seria abrir uma janela propria, o que
// arrastaria toolkit grafico para um binario que roda como servico e hoje tem
// UMA dependencia.
package main

import (
	"strings"
	"sync"
	"time"
)

const (
	// Tempo que o recado fica na tela esperando um OK. Longo de proposito: o
	// operador acabou de abrir uma sessao remota e a atencao dele esta na outra
	// maquina — um aviso que some em 10s nao seria lido.
	avisoTempoTela = 10 * time.Minute

	// Piso entre dois avisos do mesmo assunto. O servidor ja deduplica enquanto
	// o aviso esta pendente, mas o operador que reconecta ao longo do dia geraria
	// avisos novos — e sem este freio viraria uma fila de pop-ups, que e o
	// caminho mais curto para o aviso ser ignorado.
	avisoIntervaloMin = 15 * time.Minute
)

var (
	avisoMu      sync.Mutex
	avisoUltimo  time.Time
	avisoUltimoT string // titulo do ultimo mostrado, para nao repetir o mesmo
)

// mostraAviso poe o recado na sessao interativa. Nao bloqueia o worker: a API
// espera o OK do usuario ate o timeout, e o presence nao pode ficar parado
// esperando alguem clicar.
func mostraAviso(a *avisoServidor) {
	if a == nil || strings.TrimSpace(a.Mensagem) == "" {
		return
	}

	avisoMu.Lock()
	if a.Titulo == avisoUltimoT && time.Since(avisoUltimo) < avisoIntervaloMin {
		avisoMu.Unlock()
		return
	}
	avisoUltimo = time.Now()
	avisoUltimoT = a.Titulo
	avisoMu.Unlock()

	titulo := strings.TrimSpace(a.Titulo)
	if titulo == "" {
		titulo = "AcessoFast"
	}

	// A ENTREGA e por plataforma: no Windows, WTSSendMessage na sessao interativa;
	// no macOS, um dialogo pela sessao grafica do usuario. Em goroutine porque as
	// duas esperam o OK do usuario ate o timeout, e o presence nao pode ficar parado
	// esperando alguem clicar.
	go entregaAviso(titulo, a.Mensagem)
}
