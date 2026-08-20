//go:build darwin

// Espelho do plat_windows_test.go para o lado macOS. Roda no runner macOS do CI —
// a maquina Windows de quem programa nao executa isto, por isso o gate do CI roda os
// testes NOS DOIS sistemas, e nao so no de quem abriu o PR.
package main

import "testing"

// Mesma razao do teste irmao no Windows: estes sao os arquivos que a maquina do
// cliente tem no disco. Se um deles mudar de lugar numa atualizacao, o agente sobe
// sem achar token nem rustdesk_id e se comporta como recem instalado — pede matricula
// de novo numa maquina que ja estava adotada, e o suporte vai atras de um fantasma.
// Mudar qualquer caminho daqui tem que ser decisao consciente, com migracao junto,
// nunca efeito colateral de outra mexida.
func TestCaminhosDarwinNaoMudaram(t *testing.T) {
	casos := map[string]struct{ got, want string }{
		"baseDir":     {baseDir, "/Library/Application Support/AcessoFast"},
		"tokenFile":   {tokenFile, "/Library/Application Support/AcessoFast/agent.token"},
		"ridFile":     {ridFile, "/Library/Application Support/AcessoFast/rustdesk_id"},
		"logFile":     {logFile, "/Library/Application Support/AcessoFast/agent.log"},
		"pendingFile": {pendingFile, "/Library/Application Support/AcessoFast/rotate.pending"},
		"holdFile":    {holdFile, "/Library/Application Support/AcessoFast/hold.until"},
		"updateDir":   {updateDir, "/Library/Application Support/AcessoFast/update"},
	}
	for nome, c := range casos {
		if c.got != c.want {
			t.Errorf("%s = %q, esperava %q", nome, c.got, c.want)
		}
	}
}

// O rotulo do LaunchDaemon do agente e um contrato com o .pkg: o plist instalado usa
// este mesmo texto. Se divergirem, o auto-update troca o binario e NAO consegue
// reiniciar o servico — a maquina fica na versao velha rodando, sem erro visivel, ate
// alguem reiniciar na mao.
func TestRotuloDoLaunchDaemonEOContratoComOPkg(t *testing.T) {
	if agentLaunchdLabel != "br.com.acessofast.agent" {
		t.Errorf("agentLaunchdLabel = %q — se mudou de proposito, o plist do .pkg tem que mudar junto",
			agentLaunchdLabel)
	}
}
