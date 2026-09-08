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
	"unsafe"

	"golang.org/x/sys/windows"
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

	// MB_OK | MB_ICONWARNING | MB_SETFOREGROUND: um botao, icone de atencao, e a
	// janela vem para a frente. O operador esta olhando a sessao remota em tela
	// cheia; sem SETFOREGROUND o aviso nasceria atras dela.
	mbEstilo = 0x00000000 | 0x00000030 | 0x00010000
)

var (
	wtsapi32           = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSSendMessage = wtsapi32.NewProc("WTSSendMessageW")

	avisoMu      sync.Mutex
	avisoUltimo  time.Time
	avisoUltimoT string // titulo do ultimo mostrado, para nao repetir o mesmo
)

// WTS_CURRENT_SERVER_HANDLE é 0 — a maquina local.
const wtsCurrentServer = 0

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

	go func() {
		sessao := windows.WTSGetActiveConsoleSessionId()
		if sessao == 0xFFFFFFFF {
			// Ninguem logado no console (tela de bloqueio, ou maquina sem sessao).
			// O aviso volta no proximo presence: a supressao de 15 min expira
			// sozinha e o servidor so marca como entregue o que ja devolveu.
			logln("aviso nao exibido: nenhuma sessao interativa no momento")
			return
		}

		// UTF16FromString devolve o slice COM o terminador; o tamanho pedido pela
		// API e em bytes e SEM ele, dai o -1 antes de dobrar.
		tit, e1 := windows.UTF16FromString(titulo)
		msg, e2 := windows.UTF16FromString(a.Mensagem)
		if e1 != nil || e2 != nil {
			logln("aviso nao exibido: texto invalido")
			return
		}

		var resposta uint32
		ret, _, errno := procWTSSendMessage.Call(
			uintptr(wtsCurrentServer),
			uintptr(sessao),
			uintptr(unsafe.Pointer(&tit[0])),
			uintptr((len(tit)-1)*2),
			uintptr(unsafe.Pointer(&msg[0])),
			uintptr((len(msg)-1)*2),
			uintptr(mbEstilo),
			uintptr(int(avisoTempoTela.Seconds())),
			uintptr(unsafe.Pointer(&resposta)),
			0, // bWait = FALSE: nao trava esperando o clique
		)
		if ret == 0 {
			logln("aviso NAO exibido (WTSSendMessage): %v", errno)
			return
		}
		logln("aviso exibido na tela (sessao %d): %s", sessao, titulo)
	}()
}
