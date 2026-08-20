//go:build windows

// Teste da leitura da tabela TCP do Windows. Vive junto da plataforma: portaDe so
// existe onde existe o MIB do iphlpapi (plat_windows.go). No macOS o equivalente e o
// parse da saida do lsof, que tem o seu proprio teste.
package main

import "testing"

// A porta vem no DWORD do MIB em network byte order nos dois bytes baixos; ler errado
// confundiria o rendezvous (ocioso) com uma sessao e o fantasma nunca seria detectado.
func TestPortaDeLeNetworkByteOrder(t *testing.T) {
	casos := []struct {
		dword    uint32
		esperada uint16
	}{
		{0x7B52, portaNatTest},    // 21115 = 0x527B
		{0x7C52, portaRendezvous}, // 21116 = 0x527C
		{0x7D52, 21117},           // relay -> conta como sessao
		{0x5000, 80},              // porta baixa: pega inversao ingenua
	}
	for _, c := range casos {
		if got := portaDe(c.dword); got != c.esperada {
			t.Errorf("portaDe(%#x) = %d, esperava %d", c.dword, got, c.esperada)
		}
	}
}

// Os caminhos de credencial deixaram de ser constantes literais e passaram a sair de
// filepath.Join(baseDir, ...) no porte pra macOS. Sao os arquivos que a frota JA
// instalada tem no disco: se algum destes mudar, a maquina do cliente perde token,
// rustdesk_id e pendencia de senha de uma vez — e o agente se comporta como recem
// instalado, pedindo matricula de novo. Este teste existe pra que essa mudanca nunca
// passe despercebida.
func TestCaminhosWindowsNaoMudaram(t *testing.T) {
	casos := map[string]struct{ got, want string }{
		"baseDir":     {baseDir, `C:\ProgramData\AcessoFast`},
		"tokenFile":   {tokenFile, `C:\ProgramData\AcessoFast\agent.token`},
		"ridFile":     {ridFile, `C:\ProgramData\AcessoFast\rustdesk_id`},
		"logFile":     {logFile, `C:\ProgramData\AcessoFast\agent.log`},
		"pendingFile": {pendingFile, `C:\ProgramData\AcessoFast\rotate.pending`},
		"holdFile":    {holdFile, `C:\ProgramData\AcessoFast\hold.until`},
		"updateDir":   {updateDir, `C:\ProgramData\AcessoFast\update`},
	}
	for nome, c := range casos {
		if c.got != c.want {
			t.Errorf("%s = %q, esperava %q", nome, c.got, c.want)
		}
	}
}
