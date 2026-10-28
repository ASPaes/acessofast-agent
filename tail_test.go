package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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

func gravaLog(t *testing.T, caminho, conteudo string) {
	t.Helper()
	if err := os.WriteFile(caminho, []byte(conteudo), 0o644); err != nil {
		t.Fatal(err)
	}
}

func acrescentaLog(t *testing.T, caminho, conteudo string) {
	t.Helper()
	f, err := os.OpenFile(caminho, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(conteudo); err != nil {
		t.Fatal(err)
	}
}

func renomeiaLog(t *testing.T, de, para string) {
	t.Helper()
	if err := os.Rename(de, para); err != nil {
		t.Fatal(err)
	}
}

// 27/10/2026, PC do Luiz: o cliente rotacionou, o _rCURRENT.log ficou um tempo sem existir
// e voltou ja maior que o ponto onde o agente tinha parado no arquivo antigo. Pelo tamanho
// nada mudou, o agente seguiu lendo do meio do arquivo novo e o "Connection opened" do
// acesso de 28/10 09:11 ficou para tras: sem sessao, sem cobranca e sem selo da empresa.
func TestLeLogPercebeRotacaoQueNaoEncolhe(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "AcessoFast_rCURRENT.log")
	gravaLog(t, cur, strings.Repeat("linha do servidor antes do acesso\n", 6))
	tl := &tailer{open: map[string]time.Time{}, primed: true}
	if _, ok := tl.leLog(cur); !ok {
		t.Fatal("primeira leitura falhou")
	}

	acrescentaLog(t, cur, "cauda do antigo\n")
	renomeiaLog(t, cur, filepath.Join(dir, "AcessoFast_r2026-10-27_18-12-48.restart-0000.log"))
	if _, ok := tl.leLog(cur); ok {
		t.Fatal("sem o _rCURRENT.log a leitura devia falhar")
	}
	gravaLog(t, cur, "rendezvous ok\n"+
		"[INFO] #1 Connection opened from 192.168.35.111:54818.\n"+
		"[INFO] #1 peer_id 307871329\n"+
		strings.Repeat("quadro de video\n", 20))
	if fi, _ := os.Stat(cur); fi.Size() < tl.offset {
		t.Fatalf("o arquivo novo precisa nascer maior que o offset (%d < %d)", fi.Size(), tl.offset)
	}

	linhas, ok := tl.leLog(cur)
	if !ok {
		t.Fatal("leitura do arquivo novo falhou")
	}
	if len(linhas) != 24 {
		t.Fatalf("esperava a cauda + as 23 linhas do arquivo novo, veio %d: %q", len(linhas), linhas)
	}
	if linhas[0] != "cauda do antigo" || linhas[1] != "rendezvous ok" {
		t.Errorf("ordem errada: %q", linhas[:2])
	}
	if m := openedRe.FindStringSubmatch(linhas[2]); m == nil || m[1] != "1" {
		t.Errorf("o Connection opened do acesso devia vir inteiro, veio %q", linhas[2])
	}
}

// Tres rotacoes em 8 s (27/10/2026, 18:12:40 a 18:12:48): o rotacionado mais recente nao e
// o arquivo que o agente lia. A cauda tem de vir do nosso, achado pela identidade; pela
// data, o agente leria o arquivo errado a partir do offset do certo.
func TestTailRotacionadoAchaOArquivoPelaIdentidade(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "AcessoFast_rCURRENT.log")
	gravaLog(t, cur, "lido\n")
	tl := &tailer{open: map[string]time.Time{}, primed: true}
	tl.leLog(cur)

	acrescentaLog(t, cur, "[INFO] #3 Connection closed: Peer close\n")
	renomeiaLog(t, cur, filepath.Join(dir, "AcessoFast_r2026-10-27_18-12-46.log"))
	gravaLog(t, cur, "processo que durou dois segundos\n")
	renomeiaLog(t, cur, filepath.Join(dir, "AcessoFast_r2026-10-27_18-12-48.log"))
	gravaLog(t, cur, "servidor de agora\n")

	linhas, _ := tl.leLog(cur)
	esperadas := []string{"[INFO] #3 Connection closed: Peer close", "servidor de agora"}
	if !reflect.DeepEqual(linhas, esperadas) {
		t.Fatalf("esperava %q, veio %q", esperadas, linhas)
	}
}

// Truncado no lugar a identidade nao muda; o tamanho menor continua sendo o sinal, e nao ha
// cauda a buscar nos irmaos.
func TestLeLogArquivoTruncadoNoLugar(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "AcessoFast_rCURRENT.log")
	gravaLog(t, filepath.Join(dir, "AcessoFast_r2026-10-27_18-12-40.log"), "historico\n#9 Connection opened\n")
	gravaLog(t, cur, "uma linha comprida que ja foi lida\n")
	tl := &tailer{open: map[string]time.Time{}, primed: true}
	tl.leLog(cur)

	gravaLog(t, cur, "curta\n")
	linhas, _ := tl.leLog(cur)
	if !reflect.DeepEqual(linhas, []string{"curta"}) {
		t.Fatalf("esperava so a linha nova, veio %q", linhas)
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
