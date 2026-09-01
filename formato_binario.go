// AcessoFast — de que plataforma e este executavel?
//
// Sem build tag: a funcao e pura e precisa de teste rodando em qualquer maquina.
//
// EXISTE POR CAUSA DE UM CASO REAL (01/09/2026). O servidor ofereceu a um agente
// macOS o binario do WINDOWS: o catalogo so tinha releases 'windows' e a funcao que
// resolve o alvo chutava essa plataforma quando nao reconhecia o SO. O agente
// conferiu a assinatura (confere — e o mesmo arquivo, so que do outro sistema),
// conferiu o sha256 (confere), trocou o proprio binario e morreu no restart
// seguinte: o launchd nao executa um PE.
//
// Assinatura e hash provam PROCEDENCIA, nao APLICABILIDADE. Faltava perguntar se o
// arquivo sequer roda aqui.
package main

import "encoding/binary"

const (
	formatoPE           = "pe"    // Windows
	formatoMachO        = "macho" // macOS, uma arquitetura
	formatoUniversal    = "macho-universal"
	formatoDesconhecido = "desconhecido"
)

// formatoDoBinario olha os primeiros bytes e diz o que o arquivo e. Nao valida o
// resto: o objetivo nao e provar que o binario e bom, e sim recusar o que e
// obviamente de outro sistema antes de sobrescrever o agente em execucao.
func formatoDoBinario(cabecalho []byte) string {
	if len(cabecalho) >= 2 && cabecalho[0] == 'M' && cabecalho[1] == 'Z' {
		return formatoPE
	}
	if len(cabecalho) >= 4 {
		switch binary.BigEndian.Uint32(cabecalho[:4]) {
		case 0xFEEDFACE, 0xFEEDFACF, 0xCEFAEDFE, 0xCFFAEDFE:
			return formatoMachO
		case 0xCAFEBABE, 0xBEBAFECA:
			// Binario "gordo": varias arquiteturas no mesmo arquivo. E o que o
			// nosso build macOS produz (lipo de arm64 + Intel).
			return formatoUniversal
		}
	}
	return formatoDesconhecido
}

// formatoAceitoAqui diz se um formato roda nesta plataforma. A lista vem de
// plat_windows.go / plat_darwin.go.
func formatoAceitoAqui(formato string) bool {
	for _, ok := range formatosAceitos {
		if formato == ok {
			return true
		}
	}
	return false
}
