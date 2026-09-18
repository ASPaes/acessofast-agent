package main

import "testing"

// O bug de 18/09/2026 em uma linha: o servico do cliente nao segura socket nenhum,
// quem segura e o filho. Contar so a raiz devolvia 0 com o tecnico conectado, e aos
// 5 minutos o watchdog encerrava a sessao viva.
//
// A caminhada da arvore e a parte onde da para errar de novo, entao e ela que tem
// teste. A leitura do snapshot do Windows (mapaDePais) fica de fora de proposito: nao
// da para montar uma tabela de processos falsa sem virar teste do sistema operacional.

func chaves(m map[uint32]bool) []uint32 {
	out := make([]uint32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func temExatamente(t *testing.T, got map[uint32]bool, querido ...uint32) {
	t.Helper()
	if len(got) != len(querido) {
		t.Fatalf("esperava %v, veio %v", querido, chaves(got))
	}
	for _, q := range querido {
		if !got[q] {
			t.Fatalf("faltou %d; veio %v", q, chaves(got))
		}
	}
}

// O caso real: servico (3776) com um filho (15144) que e quem tem os sockets.
func TestDescendentesPegaOFilhoQueSeguraOSocket(t *testing.T) {
	pais := map[uint32]uint32{
		1216:  4,    // services.exe
		3776:  1216, // AcessoFast.exe --service  <- o que o SCM devolve
		15144: 3776, // AcessoFast.exe --server   <- quem segura a conexao
		24040: 11096,
	}
	temExatamente(t, descendentesDe(pais, 3776), 3776, 15144)
}

// Sem filhos, o resultado tem que ser a propria raiz — e nao vazio, senao a maquina
// ociosa (que so tem o servico) leria como "leitura falhou".
func TestDescendentesSemFilhosDevolveARaiz(t *testing.T) {
	pais := map[uint32]uint32{3776: 1216, 999: 1}
	temExatamente(t, descendentesDe(pais, 3776), 3776)
}

// Neto: se o cliente passar a gerar o processo de sessao a partir de um intermediario,
// a contagem nao pode parar no primeiro nivel.
func TestDescendentesDesceMaisDeUmNivel(t *testing.T) {
	pais := map[uint32]uint32{
		100: 4,
		200: 100,
		300: 200,
		400: 300,
		500: 4, // alheio
	}
	temExatamente(t, descendentesDe(pais, 100), 100, 200, 300, 400)
}

// PID reciclado pode formar laco no snapshot. Sem o conjunto de visitados isto
// travaria a goroutine do worker — nao seria contagem errada, seria agente parado.
func TestDescendentesNaoTravaComCiclo(t *testing.T) {
	pais := map[uint32]uint32{
		10: 20,
		20: 30,
		30: 10, // volta para o comeco
	}
	temExatamente(t, descendentesDe(pais, 10), 10, 20, 30)
}

// Processo que aparece como pai de si mesmo e lixo do snapshot: nao pode virar laco
// nem sumir com a raiz.
func TestDescendentesIgnoraPaiDeSiMesmo(t *testing.T) {
	pais := map[uint32]uint32{7: 7, 8: 7}
	temExatamente(t, descendentesDe(pais, 7), 7, 8)
}

// Raiz que nao existe no snapshot (processo morreu entre o SCM e o snapshot): devolve
// so ela, e o watchdog segue para a contagem de sockets, que nao achara nada. O
// resultado pratico e rearmar o relogio, nunca cortar sessao.
func TestDescendentesComRaizAusente(t *testing.T) {
	pais := map[uint32]uint32{1: 0, 2: 1}
	temExatamente(t, descendentesDe(pais, 4242), 4242)
}

// Arvores irmas nao podem se misturar: o cliente do tecnico rodando na mesma maquina
// (conexao de SAIDA) tem outra raiz e nao pode contar como sessao entrante.
func TestDescendentesNaoPegaArvoreIrma(t *testing.T) {
	pais := map[uint32]uint32{
		3776:  1216,
		15144: 3776,
		24040: 11096, // cliente aberto pelo operador
		11096: 1216,
	}
	got := descendentesDe(pais, 3776)
	if got[24040] || got[11096] {
		t.Fatalf("vazou para a arvore irma: %v", chaves(got))
	}
	temExatamente(t, got, 3776, 15144)
}
