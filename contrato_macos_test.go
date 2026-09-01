package main

import (
	"os"
	"strings"
	"testing"
)

// O .plist e o codigo tem que declarar o MESMO rotulo. Este teste le o arquivo de
// verdade, e nao uma copia da string — copia nao pega divergencia, que e justamente
// o que se quer pegar.
//
// A falha que ele evita e silenciosa: o auto-update troca o binario e nao consegue
// reiniciar o servico, entao a maquina segue na versao velha sem erro nenhum.
func TestPlistDoAgenteUsaOMesmoRotulo(t *testing.T) {
	const caminho = "installer/macos/br.com.acessofast.agent.plist"

	b, err := os.ReadFile(caminho)
	if err != nil {
		t.Fatalf("nao consegui ler %s: %v", caminho, err)
	}
	plist := string(b)

	esperado := "<string>" + rotuloDaemonAgente + "</string>"
	if !strings.Contains(plist, esperado) {
		t.Errorf("%s nao declara o rotulo %q — o instalador e o agente estao falando de servicos diferentes",
			caminho, rotuloDaemonAgente)
	}

	// O plist tambem precisa apontar para onde o instalador realmente poe o binario.
	const binario = "/Library/Application Support/AcessoFast/acessofast-agent"
	if !strings.Contains(plist, binario) {
		t.Errorf("%s nao aponta para %s — o launchd tentaria subir um caminho que nao existe",
			caminho, binario)
	}
}
