//go:build windows

// AcessoFast — agente: camada de plataforma, lado WINDOWS.
//
// Este arquivo NAO tem logica nova. Ele reune num lugar so o que existe apenas no
// Windows — servico SCM, ACL do ProgramData, tabela TCP do iphlpapi, caminhos do
// cliente branded, tarefa agendada do update — que antes vivia espalhado por
// main.go, enroll.go, rotate.go, sessao_socket.go e update.go. O par macOS
// (plat_darwin.go) implementa exatamente a mesma lista de funcoes, e o resto do
// agente (deteccao de sessao, matricula, rotacao, update) nao sabe em que SO roda.
//
// REGRA DESTE ARQUIVO: o codigo foi MOVIDO, nao reescrito. Cada funcao aqui e a
// mesma que rodava antes do porte, com o comentario que explica por que ela e assim.
// A frota Windows em producao nao pode notar diferenca nenhuma.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// baseDir: onde ficam token, rustdesk_id, agent.log, pendencia de senha e o binario
// baixado pelo auto-update. Os caminhos derivados sao montados com filepath.Join no
// codigo comum (main.go, rotate.go, update.go).
const baseDir = `C:\ProgramData\AcessoFast`

// exeSuffix: extensao do executavel neste SO. Usada pelo update ao nomear o download.
const exeSuffix = ".exe"

// clientServiceName: o servico Windows do cliente branded (o RustDesk server que
// RECEBE as conexoes). Distinto do agente (serviceName = "AcessoFastAgent").
const clientServiceName = "AcessoFast"

// updateTaskName: nome da tarefa agendada que reinicia o servico depois da troca.
const updateTaskName = "AcessoFastAgentRestart"

// clientLogPattern: glob dos logs irmaos, usado pra recuperar a cauda de um log que
// acabou de rotacionar (tailRotacionado, em main.go).
const clientLogPattern = "AcessoFast_r*.log"

// ---------------------------------------------------------------------------
// Caminhos do cliente branded
// ---------------------------------------------------------------------------

// clientConfigGlobs: onde procurar a config do cliente pra extrair o rustdesk_id.
// Namespace = AcessoFast (confirmado em maquina real: ...\AcessoFast\config\AcessoFast2.toml).
func clientConfigGlobs() []string {
	return []string{
		`C:\Windows\ServiceProfiles\LocalService\AppData\Roaming\AcessoFast\config\AcessoFast.toml`,
		`C:\Windows\ServiceProfiles\LocalService\AppData\Roaming\AcessoFast\config\AcessoFast2.toml`,
		`C:\Users\*\AppData\Roaming\AcessoFast\config\AcessoFast.toml`,
		`C:\Users\*\AppData\Roaming\AcessoFast\config\AcessoFast2.toml`,
	}
}

// clientServerLogGlobs: SO o log da subpasta "server" — a unica onde o motor escreve
// os marcadores de conexao (connection.rs, lado que RECEBE a sessao). Logs fora de
// server\ (UI / lado que controla) NAO tem esses eventos, e escolher pelo mtime ja
// cegou o agente uma vez (teste real na maquina do Ryan).
func clientServerLogGlobs() []string {
	return []string{
		`C:\Windows\ServiceProfiles\LocalService\AppData\Roaming\AcessoFast\log\server\AcessoFast_rCURRENT.log`,
		`C:\Windows\System32\config\systemprofile\AppData\Roaming\AcessoFast\log\server\AcessoFast_rCURRENT.log`,
		`C:\Users\*\AppData\Roaming\AcessoFast\log\server\AcessoFast_rCURRENT.log`,
	}
}

// findRustDeskExe localiza o executavel do cliente branded.
//
// O binario e RustDesk por baixo, mas o build branded renomeia o app-name para
// AcessoFast: o exe vira AcessoFast.exe e instala em C:\Program Files\AcessoFast
// (confirmado em maquina real, e no InstallLocation do registro de Uninstall).
// Procurar "RustDesk\rustdesk.exe" — como era antes — falha em toda maquina com
// o cliente branded, e a matricula morre aqui com exit 4.
func findRustDeskExe() (string, error) {
	candidates := []string{}

	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramW6432"} {
		if base := os.Getenv(env); base != "" {
			candidates = append(candidates, filepath.Join(base, "AcessoFast", "AcessoFast.exe"))
		}
	}
	// Fallback literal, caso as env vars estejam vazias (contexto de servico atipico).
	candidates = append(candidates,
		`C:\Program Files\AcessoFast\AcessoFast.exe`,
		`C:\Program Files (x86)\AcessoFast\AcessoFast.exe`,
	)

	seen := map[string]bool{}
	for _, p := range candidates {
		if seen[p] {
			continue
		}
		seen[p] = true
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("AcessoFast.exe nao encontrado nos caminhos padrao")
}

// ---------------------------------------------------------------------------
// Identificacao do SO
// ---------------------------------------------------------------------------

func osString() string {
	v := windows.RtlGetVersion()
	return fmt.Sprintf("Windows %d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}

// ---------------------------------------------------------------------------
// ACL do diretorio de credenciais
// ---------------------------------------------------------------------------

// hardenDir tranca o diretorio para SYSTEM + Administradores apenas.
//
// CRITICO: C:\ProgramData por padrao concede LEITURA a "Users". O agent.token e uma
// CREDENCIAL — quem le o token forja eventos de sessao daquela maquina (corrompe billing).
// Sem isso, qualquer usuario logado na maquina do cliente le o token.
//
// Usamos SIDs bem-conhecidos, NAO nomes: em Windows pt-BR "Administrators" se chama
// "Administradores" e "SYSTEM" se chama "SISTEMA" — nome literal quebraria em todo o
// nosso mercado.
func hardenDir(dir string) error {
	const (
		sidSystem         = "*S-1-5-18"     // NT AUTHORITY\SYSTEM
		sidAdministrators = "*S-1-5-32-544" // BUILTIN\Administrators
	)

	cmd := exec.Command("icacls", dir,
		"/inheritance:r",
		"/grant:r", sidSystem+":(OI)(CI)F",
		"/grant:r", sidAdministrators+":(OI)(CI)F",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("icacls falhou: %v — %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Processo do cliente branded (fontes de verdade FORA do log)
// ---------------------------------------------------------------------------

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

// clientProcStartTime devolve o horario de inicio do processo do servico do cliente
// branded (AcessoFast). Fonte FORA do log — o log nao distingue restart (conexoes
// cairam) de rotacao benigna (conexoes seguem). (time.Time{}, false) se indisponivel
// (servico parado, sem permissao): nesse caso o detector nao age.
func clientProcStartTime() (time.Time, bool) {
	pid, ok := clientProcPID()
	if !ok {
		return time.Time{}, false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return time.Time{}, false
	}
	defer windows.CloseHandle(h)
	var cre, exi, ker, usr windows.Filetime
	if err := windows.GetProcessTimes(h, &cre, &exi, &ker, &usr); err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, cre.Nanoseconds()), true
}

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

// ---------------------------------------------------------------------------
// GetExtendedTcpTable (iphlpapi) — sockets de sessao do cliente branded
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Auto-update: restart do proprio servico
// ---------------------------------------------------------------------------

// agendaRestart marca o restart do servico pra daqui a pouco. O servico nao se
// reinicia sozinho: quem chama o "net stop" morre no meio do proprio comando, e o
// "net start" nunca roda. Por isso o restart sai de um processo de fora — a tarefa
// agendada.
//
// Falhar aqui NAO e grave e por isso o chamador so loga: o binario novo JA esta no
// caminho certo, entao o proximo boot da maquina (ou qualquer restart do servico)
// ja sobe na versao nova. A tarefa so antecipa isso.
func agendaRestart() error {
	agora := time.Now()
	quando := agora.Add(2 * time.Minute)
	// Sem /sd (data), o schtasks assume hoje — e recusa uma hora que ja passou.
	// Passar a data resolveria, mas o formato de /sd segue o locale do Windows
	// (dd/MM/yyyy aqui, MM/dd/yyyy em outro) e errar isso agendaria pra data
	// errada silenciosamente. Na virada do dia, entao, simplesmente nao agendamos:
	// o proximo presence (60s) tenta de novo, ja no dia seguinte.
	if quando.Day() != agora.Day() {
		return errors.New("virada de dia: adiando o agendamento pro proximo presence")
	}
	cmd := exec.Command("schtasks", "/create",
		"/tn", updateTaskName,
		"/tr", `cmd /c net stop `+serviceName+` & net start `+serviceName,
		"/sc", "once",
		"/st", quando.Format("15:04"),
		"/ru", "SYSTEM",
		"/rl", "HIGHEST",
		"/f",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("schtasks: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Servico Windows
// ---------------------------------------------------------------------------

type service struct{}

func (s *service) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}
	stop := make(chan struct{})
	go worker(stop)
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			close(stop)
			changes <- svc.Status{State: svc.StopPending}
			return false, 0
		default:
		}
	}
	return false, 0
}

// runAgent sobe o agente como servico SCM ou, fora do SCM, em modo console/debug.
// Chamado pelo main() depois do openLog().
func runAgent() {
	isSvc, err := svc.IsWindowsService()
	if err != nil {
		logln("IsWindowsService erro: %v", err)
	}
	if !isSvc {
		logln("(modo console/debug — Ctrl+C pra sair)")
		worker(make(chan struct{}))
		return
	}
	if err := svc.Run(serviceName, &service{}); err != nil {
		logln("svc.Run erro: %v", err)
	}
}
