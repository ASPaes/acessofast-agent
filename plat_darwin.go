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
//     [.. -03:00] DEBUG [src/server/connection.rs:1377] #899 Connection opened from 192.168.35.148:50328.
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
// Da PRIMEIRA COMPILACAO do cliente branded para macOS (26/08) e do fonte do
// RustDesk 1.4.9:
//
//   - o bundle sai /Applications/AcessoFast.app, com o executavel em
//     Contents/MacOS/AcessoFast e o custom_.txt ao lado dele;
//   - os rotulos do launchd sao com.carriez.AcessoFast_service (daemon root) e
//     com.carriez.AcessoFast_server (agente da sessao grafica) — ver abaixo;
//   - o identificador do bundle e nosso (br.com.acessofast.client), separado do
//     RustDesk oficial, o que mantem as permissoes de tela e acessibilidade
//     separadas das dele.
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

// Rotulos do launchd do CLIENTE branded. Nao sao chute: saem do fonte do RustDesk
// 1.4.9 (src/platform/macos.rs), que monta os arquivos como
//
//	/Library/LaunchDaemons/{get_full_name()}_service.plist
//	/Library/LaunchAgents/{get_full_name()}_server.plist
//
// com get_full_name() = "{ORG}.{APP_NAME}" (src/common.rs). O ORG e "com.carriez" e
// o build branded NAO o troca; o APP_NAME vem do custom_.txt. Da "com.carriez.AcessoFast".
//
// SAO DOIS, e a diferenca decide como se reinicia cada um:
//
//	_service  LaunchDaemon, roda como root no dominio "system". E o que atende a
//	          tela de login (acesso antes de alguem logar).
//	_server   LaunchAgent, roda na SESSAO GRAFICA do usuario, no dominio
//	          "gui/<uid>". E ELE que recebe a sessao e escreve o log de conexao.
//
// Foi por isso que a coleta de campo (20/08) achou o log em
// /Users/<usuario>/Library/Logs e nao no home do root: quem loga a conexao vive na
// sessao do usuario. Um kickstart em "system/..." nunca alcancaria esse processo.
const (
	clientDaemonLabel = "com.carriez.AcessoFast_service"
	clientAgentLabel  = "com.carriez.AcessoFast_server"
)

// clientAppExe: o binario dentro do bundle. No macOS o cliente e um .app, e o
// executavel de verdade fica em Contents/MacOS — e ele que aceita --get-id e
// --password (confirmado no fonte do RustDesk: --password NAO e Windows-only).
const clientAppExe = "/Applications/AcessoFast.app/Contents/MacOS/AcessoFast"

// clientLogPattern: glob dos logs irmaos, usado pra recuperar a cauda de um log que
// acabou de rotacionar (tailRotacionado, em main.go).
//
// Mais frouxo que o do Windows ("AcessoFast_r*.log") de proposito: no macOS o
// PREFIXO do arquivo depende de quando o custom_.txt foi lido (ver clientServerLogGlobs),
// e um log rotacionado pode ter nascido com o outro nome. Aqui o risco de ser frouxo
// e nulo — a busca ja acontece dentro do diretorio do log corrente.
const clientLogPattern = "*_r*.log"

// ---------------------------------------------------------------------------
// Caminhos do cliente branded
// ---------------------------------------------------------------------------

// clientConfigGlobs: onde procurar a config do cliente pra extrair o rustdesk_id.
//
// COM O CLIENTE BRANDED INSTALADO (coleta de 28/08), a config saiu em
// ~/Library/Preferences/com.carriez.RustDesk/RustDesk.toml — com o nome ANTIGO,
// mesmo o app sendo o AcessoFast. Ver a explicacao em clientServerLogGlobs: o
// diretorio nasce antes de o custom_.txt ser lido.
//
// Por isso cobrimos as DUAS formas. O custo de um glob a mais e zero; o custo de
// procurar so a forma certa e nao achar e o agente sem rustdesk_id.
//
// /var/root e o home do root: e la que a config cai quando quem roda e o daemon.
func clientConfigGlobs() []string {
	var g []string
	for _, home := range []string{"/var/root", "/Users/*"} {
		for _, marca := range []string{"RustDesk", "AcessoFast"} {
			for _, arq := range []string{marca + ".toml", marca + "2.toml"} {
				g = append(g, home+"/Library/Preferences/com.carriez."+marca+"/"+arq)
			}
		}
	}
	return g
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
// O NOME DO DIRETORIO NAO E O QUE PARECE. Coleta de 28/08, com o cliente branded
// instalado, achou o log em
//
//	/Users/alexandrepaes/Library/Logs/RustDesk/AcessoFast_rCURRENT.log
//	                                  ^^^^^^^^  ^^^^^^^^^^
//	                                  antigo    novo
//
// O fonte monta o caminho como Library/Logs/{APP_NAME} (hbb_common/src/config.rs,
// log_path), entao o diretorio deveria ser AcessoFast. Ele nao e porque o diretorio
// nasce ANTES de o custom_.txt ser lido — quando APP_NAME ainda vale "RustDesk". So
// o nome do ARQUIVO, montado depois, pega o nome novo.
//
// Isso pode mudar sozinho numa atualizacao do RustDesk, nos dois sentidos. Por isso
// cobrimos as quatro combinacoes: a assimetria de hoje, o par coerente antigo e o
// par coerente novo. Errar aqui e o pior defeito possivel deste agente — ele fica
// cego, tudo parece funcionar, e nenhuma sessao e faturada.
func clientServerLogGlobs() []string {
	var g []string
	for _, home := range []string{"/var/root", "/Users/*"} {
		for _, dir := range []string{"RustDesk", "AcessoFast"} {
			for _, prefixo := range []string{"AcessoFast", "RustDesk"} {
				g = append(g, home+"/Library/Logs/"+dir+"/"+prefixo+"_rCURRENT.log")
			}
		}
	}
	return g
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

// restartClientService reinicia o cliente branded. Billing B2: e o corte DURO do
// free — derrubar quem segura a conexao derruba a sessao ativa; ao subir, o cliente
// recarrega a config com a senha JA rotacionada.
//
// kickstart -k mata e sobe numa chamada so; o -k existe pra este caso e evita a
// corrida do "stop e depois start", em que o launchd ainda esta desmontando o
// servico quando o start chega.
//
// A ORDEM importa: primeiro o LaunchAgent do usuario, que e quem segura a sessao.
// E ele NAO vive no dominio "system" — o agente roda como root, mas um LaunchAgent
// so existe dentro da sessao grafica, entao o alvo e "gui/<uid>".
func restartClientService() {
	if uid, ok := uidDoConsole(); ok {
		alvo := fmt.Sprintf("gui/%d/%s", uid, clientAgentLabel)
		out, err := exec.Command("launchctl", "kickstart", "-k", alvo).CombinedOutput()
		if err != nil {
			logln("CUT: kickstart %s falhou: %v (%s)", alvo, err, strings.TrimSpace(string(out)))
		} else {
			logln("CUT: %s reiniciado — sessao derrubada, senha nova ativa", alvo)
		}
	} else {
		logln("CUT: ninguem logado na interface grafica — nao ha LaunchAgent do cliente pra reiniciar")
	}

	// O daemon root tambem volta: e ele que atende a tela de login. Pode nem estar
	// instalado (o usuario so ativa o servico se quiser acesso desatendido), entao
	// falhar aqui nao e motivo de alarme — so registra.
	alvoDaemon := "system/" + clientDaemonLabel
	if out, err := exec.Command("launchctl", "kickstart", "-k", alvoDaemon).CombinedOutput(); err != nil {
		logln("CUT: kickstart %s: %v (%s)", alvoDaemon, err, strings.TrimSpace(string(out)))
	}
}

// uidDoConsole devolve o uid de quem esta na frente da maquina, logado na interface
// grafica. O dono de /dev/console E esse usuario — e a forma classica de descobrir
// isso no macOS, sem depender de nada instalado.
//
// (0, false) quando ninguem esta logado (maquina na tela de login) ou quando a
// leitura falha. Nesse caso nao existe LaunchAgent de usuario para reiniciar, e o
// chamador nao age — mesmo principio do resto do arquivo: quem nao sabe, nao mexe.
func uidDoConsole() (int, bool) {
	out, err := exec.Command("stat", "-f", "%u", "/dev/console").Output()
	if err != nil {
		return 0, false
	}
	uid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || uid == 0 {
		return 0, false
	}
	return uid, true
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
