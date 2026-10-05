// AcessoFast — atualiza_app.go — ATUALIZACAO DO APP (AcessoFast.exe) PELO AGENTE.
//
// O app AcessoFast (o cliente, rebrand do RustDesk) nunca se atualizou sozinho: o aviso de
// versao nova do RustDesk foi desligado e nada entrou no lugar. Este arquivo poe o agente,
// que ja roda como SYSTEM e ja sabe trocar o proprio binario (update.go), para instalar a
// versao do app que o painel mandar.
//
// O canal e o mesmo do auto-update do agente:
//   - todo presence leva client_version (o que esta instalado);
//   - o servidor resolve o alvo (device -> tenant -> global, tabela client_releases) e, se
//     for diferente, devolve client_update {version, url, sha256, signature};
//   - o agente confere a assinatura ANTES de baixar, confere o sha256 depois, e so instala
//     com a maquina ociosa.
//
// A assinatura usa a MESMA chave do agente mas outro prefixo ("acessofast-client:v1:"): uma
// assinatura de release do agente nao vale como release do app, e vice-versa.
//
// Instalar = rodar o executavel unico do app com --silent-install, como o instalador de
// producao faz. Por cima do AcessoFast instalado, o RustDesk para o servico, troca a pasta
// de Program Files e sobe o servico de novo; a config (ID, senha) mora em outra pasta e nao
// e tocada. Testado em 01/10/2026 no PC do Ryan: ID 307871329 antes e depois, servico de
// volta em 10s, agente seguiu matriculado.
//
// Versao do app: arquivo acessofast-versao.txt na pasta do app (o build do app novo grava,
// ex.: 2026.10.01-875ebe8). O AcessoFast antigo nao tem o arquivo e e reportado como
// "legado" — e esse o publico da primeira troca.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	appVersaoArquivo = "acessofast-versao.txt"
	appVersaoLegado  = "legado"
	// Quanto esperar o app novo ficar de pe (servico rodando E arquivo de versao novo). No
	// teste real foram 10s; o teto cobre maquina lenta sem prender o worker para sempre.
	appInstalaTimeout = 3 * time.Minute
)

// Estado por execucao, como o do update do agente (update.go): so a goroutine do worker
// toca, entao sem mutex.
var (
	appUpdateTries    = map[string]int{}
	appUpdateAplicado string
	// Bloco client_update do ultimo presence. O postEventFull guarda e o worker aplica
	// depois do update do agente, no mesmo tick (maquina ociosa).
	appUpdatePendente *updateInfo
)

// versaoDoApp devolve a versao do app instalado: o conteudo de acessofast-versao.txt, ou
// "legado" para o AcessoFast antigo (sem o arquivo). Vazio = app nao encontrado — nesse
// caso o agente nao reporta nada e o servidor nao oferece atualizacao.
func versaoDoApp() string {
	exe, err := findRustDeskExe()
	if err != nil {
		return ""
	}
	return versaoNaPasta(filepath.Dir(exe))
}

func versaoNaPasta(pasta string) string {
	b, err := os.ReadFile(filepath.Join(pasta, appVersaoArquivo))
	if err != nil {
		return appVersaoLegado
	}
	v := strings.TrimSpace(string(b))
	// Rotulo de build, nao texto livre: algo estranho no arquivo nao vira versao.
	if v == "" || len(v) > 40 || strings.ContainsAny(v, " \t\r\n") {
		return appVersaoLegado
	}
	return v
}

// manifestoCanonicoApp e EXATAMENTE a string que o CI assina para um release do app.
func manifestoCanonicoApp(version, sha256hex string) string {
	return "acessofast-client:v1:" + version + ":" + strings.ToLower(sha256hex)
}

// motivoPularApp decide se o bloco deve ser IGNORADO; "" = pode aplicar. Pura, como a
// motivoPular do agente, para ser testada sozinha.
func motivoPularApp(u *updateInfo, atual, jaAplicado string, tentativas int) string {
	switch {
	case u == nil || u.Version == "" || u.URL == "" || u.SHA256 == "" || u.Signature == "":
		return "manifesto incompleto"
	case atual == "":
		return "app nao encontrado nesta maquina"
	case u.Version == atual:
		return "ja estamos nesta versao"
	case u.Version == jaAplicado:
		return "ja instalada nesta execucao"
	case tentativas >= updateMaxTries:
		return "falhou demais nesta execucao"
	}
	return ""
}

// pegaAtualizacaoDoApp entrega (e limpa) o bloco recebido no presence.
func pegaAtualizacaoDoApp() *updateInfo {
	u := appUpdatePendente
	appUpdatePendente = nil
	return u
}

// aplicaAtualizacaoDoApp roda o fluxo inteiro. Chamado SO no ramo de presence do worker,
// inline: enquanto instala, o worker nao faz mais nada (nem a autocura do vigia_cliente,
// que de outro modo poderia tentar subir o servico no meio da troca).
func aplicaAtualizacaoDoApp(u *updateInfo) {
	if u == nil {
		return
	}
	atual := versaoDoApp()
	if motivo := motivoPularApp(u, atual, appUpdateAplicado, appUpdateTries[u.Version]); motivo != "" {
		return
	}

	// Ultima conferencia de sessao, direto nos sockets: o presence so sai sem sessao no log,
	// mas instalar derruba o servico, e derrubar um atendimento e o erro caro. Leitura
	// duvidosa conta como "tem sessao": adia para o proximo presence.
	if pids, ok := pidsDoCliente(); ok {
		if n, ok := socketsDeSessao(pids); !ok || n > 0 {
			logln("app %s: adiado (sessao em andamento ou leitura duvidosa)", u.Version)
			return
		}
	}

	appUpdateTries[u.Version]++

	if err := verificaAssinaturaDe(u, manifestoCanonicoApp); err != nil {
		logln("app %s REPROVADO na assinatura: %v (nada foi baixado)", u.Version, err)
		return
	}
	logln("app %s: assinatura confere, baixando de %s (instalado: %s)", u.Version, u.URL, atual)

	verificado, err := baixaEConfereComo(u, "acessofast-app-"+u.Version+".exe")
	if err != nil {
		logln("app %s falhou no download/hash: %v (tentativa %d/%d)",
			u.Version, err, appUpdateTries[u.Version], updateMaxTries)
		return
	}

	exe, _ := findRustDeskExe()
	idAntes := rustdeskID

	// --silent-install nao retorna sozinho (o instalador de producao tambem so espera e
	// confere): dispara, nao espera o processo, e confere o resultado.
	cmd := exec.Command(verificado, "--silent-install")
	if err := cmd.Start(); err != nil {
		logln("app %s: nao consegui iniciar o instalador: %v", u.Version, err)
		return
	}
	go func() { _ = cmd.Wait() }()
	logln("app %s: instalando por cima de %s", u.Version, atual)

	ok := false
	fim := time.Now().Add(appInstalaTimeout)
	for time.Now().Before(fim) {
		time.Sleep(5 * time.Second)
		if clienteVivo() && versaoDoApp() == u.Version {
			ok = true
			break
		}
	}
	if !ok {
		logln("app %s: a instalacao nao se confirmou em %s (servico vivo=%v, versao=%s; tentativa %d/%d)",
			u.Version, appInstalaTimeout, clienteVivo(), versaoDoApp(), appUpdateTries[u.Version], updateMaxTries)
		return
	}
	appUpdateAplicado = u.Version

	// O ID tem que ser o mesmo: e ele que liga a maquina ao cadastro no painel. Se mudar, a
	// revalidacao do main.go (revalidaRustdeskID) cuida da rematricula, mas fica registrado.
	if exe != "" {
		if id, err := getRustDeskID(exe); err != nil {
			logln("app %s instalado; nao consegui reler o ID: %v", u.Version, err)
		} else if id != idAntes {
			logln("ALERTA app %s instalado e o ID MUDOU: %s -> %s", u.Version, idAntes, id)
		} else {
			logln("app %s instalado; ID %s mantido", u.Version, id)
		}
	}
}
