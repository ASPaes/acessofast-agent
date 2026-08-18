package main

import (
	"strings"
	"testing"
)

// O caso que motivou o buffer (fantasma da DESKTOP-3SL0480, 18/08/2026): o poll de 3s cai
// no meio da linha de fechamento. Antes, os dois fragmentos passavam pelos regex sem casar
// e o #N ficava preso ate as 24h do expireStale.
func TestFatiaLinhasRemontaLinhaPartida(t *testing.T) {
	tl := &tailer{}

	if l := tl.fatiaLinhas("[info] #7 Connection clo"); len(l) != 0 {
		t.Fatalf("pedaco sem newline nao devia render linha: %q", l)
	}
	if tl.pending == "" {
		t.Fatal("o pedaco parcial devia ter ficado no buffer")
	}

	linhas := tl.fatiaLinhas("sed\n[info] #8 Connection opened\n")
	if len(linhas) != 2 {
		t.Fatalf("esperava 2 linhas completas, veio %d: %q", len(linhas), linhas)
	}
	if !closedRe.MatchString(linhas[0]) {
		t.Errorf("linha remontada nao casa com closedRe: %q", linhas[0])
	}
	if m := closedRe.FindStringSubmatch(linhas[0]); m == nil || m[1] != "7" {
		t.Errorf("esperava a conexao #7, veio %q", linhas[0])
	}
	if tl.pending != "" {
		t.Errorf("chunk terminado em newline nao deixa sobra, veio %q", tl.pending)
	}
}

func TestFatiaLinhasGuardaSobraEntreTicks(t *testing.T) {
	tl := &tailer{}

	linhas := tl.fatiaLinhas("a\nb\nc parcial")
	if len(linhas) != 2 || linhas[0] != "a" || linhas[1] != "b" {
		t.Fatalf("esperava [a b], veio %q", linhas)
	}
	if tl.pending != "c parcial" {
		t.Fatalf("sobra errada: %q", tl.pending)
	}
	if linhas := tl.fatiaLinhas("\n"); len(linhas) != 1 || linhas[0] != "c parcial" {
		t.Fatalf("a sobra devia fechar no tick seguinte, veio %q", linhas)
	}
}

// Sem teto, um arquivo sem newline (binario, escrita travada) faria o buffer crescer
// para sempre dentro de um servico que nunca reinicia.
func TestFatiaLinhasDescartaBufferGigante(t *testing.T) {
	tl := &tailer{}

	tl.fatiaLinhas(strings.Repeat("x", maxPendingBytes+1))
	if tl.pending != "" {
		t.Fatalf("buffer acima do teto devia ser descartado, sobraram %d bytes", len(tl.pending))
	}
	tl.fatiaLinhas("ok\n" + strings.Repeat("y", maxPendingBytes+1))
	if tl.pending != "" {
		t.Fatalf("sobra acima do teto devia ser descartada, sobraram %d bytes", len(tl.pending))
	}
}

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
