package main

import "testing"

// Os bytes sao os de verdade: "MZ" abre todo executavel do Windows, e os quatro
// magicos do Mach-O sao os que a Apple documenta.
func TestFormatoDoBinario(t *testing.T) {
	casos := []struct {
		nome     string
		bytes    []byte
		esperado string
	}{
		{"exe do Windows", []byte{'M', 'Z', 0x90, 0x00}, formatoPE},
		{"Mach-O 64 bits", []byte{0xCF, 0xFA, 0xED, 0xFE}, formatoMachO},
		{"Mach-O 32 bits", []byte{0xCE, 0xFA, 0xED, 0xFE}, formatoMachO},
		{"Mach-O universal", []byte{0xCA, 0xFE, 0xBA, 0xBE}, formatoUniversal},
		{"vazio", nil, formatoDesconhecido},
		{"curto demais", []byte{'M'}, formatoDesconhecido},
		{"texto", []byte("<!DOCTYPE html>"), formatoDesconhecido},
	}
	for _, c := range casos {
		if got := formatoDoBinario(c.bytes); got != c.esperado {
			t.Errorf("%s: formatoDoBinario = %q, esperava %q", c.nome, got, c.esperado)
		}
	}
}

// O CASO REAL de 01/09/2026: o servidor ofereceu o .exe do Windows a um Mac. Este
// teste fixa que cada plataforma so aceita o que ela consegue executar.
func TestFormatoAceitoAquiRecusaODaOutraPlataforma(t *testing.T) {
	if !formatoAceitoAqui(formatoDoBinario([]byte{'M', 'Z'})) &&
		!formatoAceitoAqui(formatoUniversal) && !formatoAceitoAqui(formatoMachO) {
		t.Fatal("esta plataforma nao aceita formato nenhum — a lista esta vazia")
	}
	if formatoAceitoAqui(formatoDesconhecido) {
		t.Error("formato desconhecido nunca pode ser aceito")
	}
	// Um dos dois tem que ser recusado: nenhuma plataforma roda os dois.
	aceitaPE := formatoAceitoAqui(formatoPE)
	aceitaMach := formatoAceitoAqui(formatoMachO)
	if aceitaPE == aceitaMach {
		t.Errorf("PE e Mach-O nao podem ter o mesmo veredito (pe=%v macho=%v)", aceitaPE, aceitaMach)
	}
}
