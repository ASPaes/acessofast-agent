//go:build darwin

// AcessoFast — agente: camada de plataforma, lado macOS.
//
// Mesma lista de funcoes do plat_windows.go, implementada com o que o macOS tem:
//
//	servico SCM          -> LaunchDaemon (launchctl)
//	ACL por icacls/SID   -> dono root + modo 0700
//	GetExtendedTcpTable  -> lsof
//	GetProcessTimes      -> ps -o lstart
//	tarefa agendada      -> kickstart adiado (o launchd tem KeepAlive)
//
// O resto do agente (deteccao de sessao, matricula, rotacao de senha, auto-update)
// e o MESMO codigo que roda no Windows: a regra vive nos arquivos comuns, so a
// leitura do sistema muda aqui.
//
// ---------------------------------------------------------------------------
// COLETA DE CAMPO — 20/08/2026, MacBook Pro (macOS 26.5.2, arm64), RustDesk 1.4.9
//
// O que ficou PROVADO em maquina real:
//
//   - Os marcadores de sessao EXISTEM no macOS, no mesmo formato do Windows e
//     vindos do mesmo lugar do fonte:
//       [.. -03:00] DEBUG [src/server/connection.rs:1377] #899 Connection opened from 192.168.35.148:50328.
//     A deteccao de sessao do agente vale nos dois sistemas.
//
//   - O log NAO fica numa subpasta "server", como no Windows. Fica na RAIZ de
//     ~/Library/Logs/<AppName>/. As subpastas que existem (cm/, check-hwcodec-config/)
//     NAO tem os marcadores.
//
//   - A config fica em ~/Library/Preferences/com.carriez.<AppName>/.
//
//   - O `ps` responde no IDIOMA da maquina: naquele Mac, "qui 20 ago 17:40:23 2026".
//     Por isso o comando roda com LC_ALL=C (ver clientProcStartTime).
//
// AINDA POR CONFIRMAR: onde o log cai quando o cliente roda como SERVICO instalado.
// Na coleta o app rodava a partir do DMG montado, sem servico nenhum registrado no
// launchd — entao o que se viu foi o log da sessao do USUARIO. Por isso os globs
// abaixo cobrem tambem /var/root, que e o home do root.
// ---------------------------------------------------------------------------
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// baseDir: equivalente do C:\ProgramData\AcessoFast. /Library/Application Support e
// o lugar do macOS para dado de aplicacao com escopo de MAQUINA (nao de usuario) —
// o agente roda como LaunchDaemon root e a credencial nao pode viver no home de
// ninguem.
const baseDir = "/Library/Application Support/AcessoFast"

// exeSuffix: no macOS o executavel nao tem extensao.
const exeSuffix = ""

// agentLaunchdLabel: rotulo do LaunchDaemon do AGENTE. O .plist instalado pelo .pkg
// usa este mesmo rotulo; se os dois divergirem, o auto-update troca o binario e
// nunca consegue reiniciar o servico.
const agentLaunchdLabel = "br.com.acessofast.agent"

// clientLaunchdLabel: rotulo do LaunchDaemon do CLIENTE branded (o RustDesk server
// que RECEBE as conexoes). CONFIRMAR: o RustDesk usa ORG "com.carriez" + APP_NAME
// no macOS, e o build branded troca o APP_NAME para AcessoFast.
const clientLaunchdLabel = "com.carriez.AcessoFast"

// clientAppExe: o binario dentro do bundle. No macOS o cliente e um .app, e o
// executavel de verdade fica em Contents/MacOS — e ele que aceita --get-id e
// --password (confirmado no fonte do RustDesk: --password NAO e Windows-only).
const clientAppExe = "/Applications/AcessoFast.app/Contents/MacOS/AcessoFast"

// clientLogPattern: glob dos logs irmaos, usado pra recuperar a cauda de um log que
// acabou de rotacionar (tailRotacionado, em main.go).
const clientLogPattern = "AcessoFast_r*.log"

// ---------------------------------------------------------------------------
// Caminhos do cliente branded
// ---------------------------------------------------------------------------

// clientConfigGlobs: onde procurar a config do cliente pra extrair o rustdesk_id.
//
// Caminho CONFIRMADO em campo (20/08/2026): o RustDesk 1.4.9 no macOS grava em
// ~/Library/Preferences/com.carriez.<AppName>/<AppName>.toml — no teste,
// /Users/vinipaes/Library/Preferences/com.carriez.RustDesk/RustDesk.toml. O build
// branded troca o AppName para AcessoFast, entao o formato abaixo e o mesmo.
//
// /var/root e o home do root: e la que a config cai quando quem roda e o daemon.
func clientConfigGlobs() []string {
	return []string{
		"/var/root/Library/Preferences/com.carriez.AcessoFast/AcessoFast.toml",
		"/var/root/Library/Preferences/com.carriez.AcessoFast/AcessoFast2.toml",
		"/Users/*/Library/Preferences/com.carriez.AcessoFast/AcessoFast.toml",
		"/Users/*/Library/Preferences/com.carriez.AcessoFast/AcessoFast2.toml",
	}
}

// clientServerLogGlobs: o log que carrega os marcadores de conexao.
//
// AQUI O macOS DIFERE DO WINDOWS, e a coleta de campo (20/08/2026) desmentiu o que
// eu tinha deduzido. No Windows os marcadores so existem no log da subpasta
// "server\", e o log da raiz e do lado que CONTROLA — por isso la a busca exige
// server\. No macOS NAO EXISTE essa subpasta: o "#N Connection opened" sai no log da
// RAIZ, ~/Library/Logs/<AppName>/<AppName>_rCURRENT.log.
//
// As subpastas que existem no macOS sao outras — cm/ (o gerenciador de conexoes, que
// loga conn_id e nao os marcadores) e check-hwcodec-config/ (teste de codec) — e
// nenhuma serve. Por isso o glob nomeia o ARQUIVO exato num nivel exato: qualquer
// coisa dentro de subpasta fica de fora sozinha, sem precisar de filtro.
//
// Os dois caminhos: o do usuario logado (onde o cliente roda a sessao grafica) e o do
// root (se o servico instalado gravar no home dele). O agente roda como root e le os
// dois. Qual deles vale com o servico instalado ainda esta por confirmar — na coleta
// o app rodava direto do DMG, sem servico registrado.
func clientServerLogGlobs() []string {
	return []string{
		"/var/root/Library/Logs/AcessoFast/AcessoFast_rCURRENT.log",
		"/Users/*/Library/Logs/AcessoFast/AcessoFast_rCURRENT.log",
	}
}

// findRustDeskExe localiza o executavel do cliente branded dentro do bundle .app.
func findRustDeskExe() (string, error) {
	if fi, err := os.Stat(clientAppExe); err == nil && !fi.IsDir() {
		return clientAppExe, nil
	}
	return "", fmt.Errorf("%s nao encontrado — o cliente AcessoFast nao esta instalado", clientAppExe)
}

// ---------------------------------------------------------------------------
// Identificacao do SO
// ---------------------------------------------------------------------------

func osString() string {
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		return "macOS"
	}
	return "macOS " + strings.TrimSpace(string(out))
}

// ---------------------------------------------------------------------------
// Permissao do diretorio de credenciais
// ---------------------------------------------------------------------------

// hardenDir tranca o diretorio para o root apenas.
//
// Mesmo motivo do Windows: /Library/Application Support e legivel por qualquer
// usuario por padrao, e o agent.token e uma CREDENCIAL — quem le o token forja
// eventos de sessao daquela maquina e corrompe o faturamento. Aqui isso e dono root
// (uid 0, gid 0) mais modo 0700, que e o equivalente do icacls com os dois SIDs.
func hardenDir(dir string) error {
	if err := os.Chown(dir, 0, 0); err != nil {
		return fmt.Errorf("chown root em %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod 0700 em %s: %w", dir, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Processo do cliente branded (fontes de verdade FORA do log)
// ---------------------------------------------------------------------------

// clientProcPID devolve o PID do processo do cliente branded. (0, false) quando ele
// nao esta rodando — nesse caso ninguem age.
//
// pgrep -f casa pelo caminho COMPLETO do executavel, e nao pelo nome: o bundle pode
// ter processos auxiliares e derrubar a sessao por causa do processo errado seria o
// tipo de erro que so aparece na maquina do cliente.
func clientProcPID() (uint32, bool) {
	out, err := exec.Command("pgrep", "-f", clientAppExe).Output()
	if err != nil {
		return 0, false
	}
	for _, linha := range strings.Fields(string(out)) {
		pid, err := strconv.ParseUint(strings.TrimSpace(linha), 10, 32)
		if err == nil && pid > 0 {
			return uint32(pid), true
		}
	}
	return 0, false
}

// clientProcStartTime devolve o horario de inicio do processo do cliente branded.
// Fonte FORA do log — o log nao distingue restart (conexoes cairam) de rotacao
// benigna (conexoes seguem). (time.Time{}, false) se indisponivel: nesse caso o
// detector nao age.
func clientProcStartTime() (time.Time, bool) {
	pid, ok := clientProcPID()
	if !ok {
		return time.Time{}, false
	}
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.FormatUint(uint64(pid), 10))
	// LC_ALL=C NAO e enfeite. O ps responde no idioma da maquina, e o Mac do teste
	// (pt-BR) devolveu "qui 20 ago 17:40:23 2026" — dia antes do mes, nomes em
	// portugues. Sem forcar o idioma neutro, o parser falharia em toda maquina que
	// nao esteja em ingles, ou seja, em praticamente todo cliente nosso. E o agente
	// perderia o sinal de restart do cliente, deixando #N preso (fantasma).
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, false
	}
	// lstart sai como "Wed Aug 20 08:14:33 2026", no fuso local da maquina. A
	// interpretacao vive em saida_macos.go, de proposito SEM build tag: assim ela
	// tem teste rodando em qualquer maquina, e nao so num Mac.
	return parseLstart(string(out), time.Local)
}

// restartClientService reinicia o LaunchDaemon do cliente branded. Billing B2: e o
// corte DURO do free — derrubar o daemon derruba a sessao ativa; ao subir, o cliente
// recarrega a config com a senha JA rotacionada.
//
// kickstart -k mata e sobe de novo numa chamada so; o -k existe exatamente pra este
// caso e evita a corrida do "stop e depois start" (em que o launchd ainda esta
// desmontando o servico quando o start chega).
func restartClientService() {
	alvo := "system/" + clientLaunchdLabel
	out, err := exec.Command("launchctl", "kickstart", "-k", alvo).CombinedOutput()
	if err != nil {
		logln("CUT: launchctl kickstart %s falhou: %v (%s)", alvo, err, strings.TrimSpace(string(out)))
		return
	}
	logln("CUT: daemon %s reiniciado — sessao derrubada, senha nova ativa", clientLaunchdLabel)
}

// ---------------------------------------------------------------------------
// Sockets de sessao do cliente branded (equivalente do GetExtendedTcpTable)
// ---------------------------------------------------------------------------

// socketsDeSessao conta os TCP ESTABLISHED do PID que NAO sao o vinculo do
// rendezvous. (0, false) = leitura falhou; o chamador nao age nesse caso (fail-safe).
//
// lsof no lugar da tabela do kernel: o macOS nao expoe nada como o GetExtendedTcpTable,
// e netstat no macOS NAO mostra o dono do socket. O -F devolve saida por campo (uma
// letra por linha), que e estavel entre versoes — bem mais seguro de parsear do que a
// tabela humana do lsof.
func socketsDeSessao(pid uint32) (int, bool) {
	out, err := exec.Command("lsof",
		"-nP", // sem resolver DNS nem nome de porta (rapido e estavel)
		"-a",  // combina os filtros abaixo em E, nao em OU
		"-p", strconv.FormatUint(uint64(pid), 10),
		"-iTCP", "-sTCP:ESTABLISHED",
		"-Fn", // so o campo de nome (endereco), uma linha por socket
	).Output()
	if err != nil {
		// lsof sai com 1 quando NAO ha nenhum socket casando o filtro — e o caso
		// legitimo de maquina ociosa, nao uma falha de leitura. Distinguimos pelo
		// exit code: 1 com saida vazia = zero sockets; qualquer outra coisa = nao
		// conseguimos ler, e ai o chamador nao pode agir.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 && len(out) == 0 {
			return 0, true
		}
		return 0, false
	}

	// A CONTAGEM vive em saida_macos.go, de proposito SEM build tag. E a parte que
	// erra EM SILENCIO — um socket a mais e a sessao nunca termina (fantasma), um a
	// menos e a sessao de quem esta trabalhando cai. Fora da tag, ela tem teste
	// rodando em qualquer maquina; sob a tag, so num Mac.
	return contaSocketsSessao(string(out)), true
}

// ---------------------------------------------------------------------------
// Auto-update: restart do proprio daemon
// ---------------------------------------------------------------------------

// agendaRestart faz o agente voltar a subir ja no binario novo.
//
// No Windows isso exige uma tarefa agendada, porque quem chama o "net stop" morre no
// meio do proprio comando. Aqui o problema e o mesmo — um kickstart no proprio
// servico mataria este processo antes de ele terminar — e a solucao e a mesma ideia:
// o restart sai de um processo DE FORA, que espera e so entao chuta o daemon. O
// launchd sobe de volta sozinho (KeepAlive no .plist).
//
// A espera de 2 minutos e a mesma do Windows, pelo mesmo motivo: o binario ja esta
// trocado no disco, entao nao ha pressa, e um restart imediato cortaria a resposta
// do presence que acabou de chegar.
func agendaRestart() error {
	alvo := "system/" + agentLaunchdLabel
	cmd := exec.Command("/bin/sh", "-c",
		fmt.Sprintf("sleep 120; launchctl kickstart -k %s", alvo))
	// Setsid: o filho sobrevive a morte deste processo. Sem isso o kickstart morreria
	// junto com o agente que ele deveria reiniciar.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("agendar kickstart: %w", err)
	}
	// Nao esperamos o filho: ele vive 2 minutos alem daqui. Release evita deixar
	// zumbi caso este processo continue vivo (o caso comum, ate o restart chegar).
	if err := cmd.Process.Release(); err != nil {
		logln("update: nao consegui soltar o processo do restart: %v", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// LaunchDaemon
// ---------------------------------------------------------------------------

// runAgent roda o agente em foreground, que e como o launchd espera: ele NAO deve se
// daemonizar (nada de fork), porque quem supervisiona o processo e o proprio launchd.
// Ao parar o servico, o launchd manda SIGTERM — traduzimos isso no mesmo 'stop' que o
// SCM entrega no Windows, pra que o desligamento passe pelo mesmo caminho de codigo.
func runAgent() {
	stop := make(chan struct{})

	sinais := make(chan os.Signal, 1)
	signal.Notify(sinais, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-sinais
		logln("sinal %v recebido — encerrando", s)
		close(stop)
	}()

	worker(stop)
}
