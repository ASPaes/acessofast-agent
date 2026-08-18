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

package main

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
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

// clientProcPID devolve o PID do servico do cliente branded. (0, false) quando o
// servico esta parado ou o SCM nao responde — nesses casos ninguem age.
func clientProcPID() (uint32, bool) {
	m, err := mgr.Connect()
	if err != nil {
		return 0, false
	}
	defer m.Disconnect()
	s, err := m.OpenService(clientServiceName)
	if err != nil {
		return 0, false
	}
	defer s.Close()
	st, err := s.Query()
	if err != nil || st.ProcessId == 0 {
		return 0, false
	}
	return st.ProcessId, true
}

// ---- GetExtendedTcpTable (iphlpapi) ----------------------------------------------

var (
	modIphlpapi             = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTCPTable = modIphlpapi.NewProc("GetExtendedTcpTable")
)

const (
	afInet                      = 2
	afInet6                     = 23
	tcpTableOwnerPIDConnections = 4
	mibTCPStateEstab            = 5
)

// MIB_TCPROW_OWNER_PID (IPv4).
type mibTCPRowOwnerPID struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
	OwningPID  uint32
}

// MIB_TCP6ROW_OWNER_PID (IPv6). Consultado junto com o IPv4: enxergar so metade das
// familias faria um socket v6 legitimo parecer ausencia de sessao — falso positivo na
// direcao cara.
type mibTCP6RowOwnerPID struct {
	LocalAddr     [16]byte
	LocalScopeID  uint32
	LocalPort     uint32
	RemoteAddr    [16]byte
	RemoteScopeID uint32
	RemotePort    uint32
	State         uint32
	OwningPID     uint32
}

// portaDe converte o DWORD do MIB (porta em network byte order nos dois bytes baixos).
func portaDe(v uint32) uint16 {
	return uint16(v&0xFF)<<8 | uint16((v>>8)&0xFF)
}

// tabelaTCP le a tabela de conexoes com dono (PID) de uma familia. A tabela pode crescer
// entre o dimensionamento e a leitura, por isso o retry.
func tabelaTCP(family uintptr) ([]byte, bool) {
	for tentativa := 0; tentativa < 3; tentativa++ {
		var size uint32
		r, _, _ := procGetExtendedTCPTable.Call(
			0, uintptr(unsafe.Pointer(&size)), 0, family, tcpTableOwnerPIDConnections, 0)
		if r != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) && r != 0 {
			return nil, false
		}
		if size < 4 {
			size = 4
		}
		buf := make([]byte, size)
		r, _, _ = procGetExtendedTCPTable.Call(
			uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0,
			family, tcpTableOwnerPIDConnections, 0)
		if r == 0 {
			return buf, true
		}
		if r != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
			return nil, false
		}
	}
	return nil, false
}

// socketsDeSessao conta os TCP ESTABLISHED do PID que NAO sao o vinculo do rendezvous.
// (0, false) = leitura falhou; o chamador nao age nesse caso (fail-safe).
func socketsDeSessao(pid uint32) (int, bool) {
	total := 0
	for _, fam := range []uintptr{afInet, afInet6} {
		buf, ok := tabelaTCP(fam)
		if !ok {
			return 0, false
		}
		if len(buf) < 4 {
			continue
		}
		n := *(*uint32)(unsafe.Pointer(&buf[0]))
		rowSz := unsafe.Sizeof(mibTCPRowOwnerPID{})
		if fam == afInet6 {
			rowSz = unsafe.Sizeof(mibTCP6RowOwnerPID{})
		}
		if uintptr(len(buf)) < 4+uintptr(n)*rowSz {
			return 0, false // tabela menor do que anuncia: nao confia
		}
		for i := uintptr(0); i < uintptr(n); i++ {
			p := unsafe.Pointer(&buf[4+i*rowSz])
			var estado, remota uint32
			var dono uint32
			if fam == afInet {
				r := (*mibTCPRowOwnerPID)(p)
				estado, remota, dono = r.State, r.RemotePort, r.OwningPID
			} else {
				r := (*mibTCP6RowOwnerPID)(p)
				estado, remota, dono = r.State, r.RemotePort, r.OwningPID
			}
			if dono != pid || estado != mibTCPStateEstab {
				continue
			}
			switch portaDe(remota) {
			case portaNatTest, portaRendezvous:
				continue // maquina ociosa falando com o servidor
			}
			total++
		}
	}
	return total, true
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
	t.semSocketDesde = time.Time{}
	t.open = make(map[string]time.Time)
	t.graceUntil = time.Time{}
	t.hardCapUntil = time.Time{}
	t.controllerID = ""
	postEvent("end", "")
	go rotateNow()
}
