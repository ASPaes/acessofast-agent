// Testes da interpretacao da saida do macOS. Sem build tag, pelo mesmo motivo do
// saida_macos.go: rodam em qualquer maquina, inclusive no CI do Windows. E o unico
// jeito de o lado Mac ter rede de protecao antes de existir um Mac no processo.
package main

import (
	"testing"
	"time"
)

// A saida real do lsof -Fn: uma letra por linha. A linha 'p' e o pid, as 'n' sao os
// enderecos. Este caso junta tudo o que uma maquina em atendimento mostra: o vinculo
// ocioso com o rendezvous, o socket do relay e um acesso direto.
func TestContaSocketsSessaoSeparaSessaoDeRendezvous(t *testing.T) {
	saida := "p512\n" +
		"n192.168.0.5:52200->203.0.113.9:21116\n" + // rendezvous: NAO e sessao
		"n192.168.0.5:52201->203.0.113.9:21115\n" + // teste de NAT: NAO e sessao
		"n192.168.0.5:52341->203.0.113.9:21117\n" + // relay: E sessao
		"n192.168.0.5:52999->198.51.100.7:41234\n" // acesso direto: E sessao

	if got := contaSocketsSessao(saida); got != 2 {
		t.Errorf("contaSocketsSessao = %d, esperava 2 (relay + direto)", got)
	}
}

// Maquina ligada e ociosa: so o vinculo com o servidor. Tem que dar ZERO — se desse
// 1, nenhuma sessao fantasma seria detectada, que e o bug que este mecanismo existe
// pra fechar.
func TestContaSocketsSessaoMaquinaOciosaDaZero(t *testing.T) {
	saida := "p512\nn192.168.0.5:52200->203.0.113.9:21116\n"
	if got := contaSocketsSessao(saida); got != 0 {
		t.Errorf("maquina ociosa deu %d socket(s) de sessao, esperava 0", got)
	}
}

// IPv6 vem entre colchetes e cheio de dois-pontos. Ler a porta do primeiro ":" daria
// lixo e a sessao v6 legitima sumiria — falso positivo de fantasma, que ENCERRA a
// sessao de quem esta trabalhando.
func TestContaSocketsSessaoEntendeIPv6(t *testing.T) {
	casos := []struct {
		nome     string
		linha    string
		esperado int
	}{
		{"v6 no relay", "n[2804:14d::1]:52341->[2001:db8::1]:21117", 1},
		{"v6 no rendezvous", "n[2804:14d::1]:52200->[2001:db8::1]:21116", 0},
	}
	for _, c := range casos {
		if got := contaSocketsSessao(c.linha + "\n"); got != c.esperado {
			t.Errorf("%s: contaSocketsSessao = %d, esperava %d", c.nome, got, c.esperado)
		}
	}
}

// Nada que nao seja um socket conectado pode contar como sessao.
func TestContaSocketsSessaoIgnoraOQueNaoEConexao(t *testing.T) {
	casos := []struct {
		nome  string
		saida string
	}{
		{"saida vazia", ""},
		{"so o pid", "p512\n"},
		{"socket em escuta (sem ->)", "p512\nn*:21118\n"},
		{"linha de campo desconhecido", "p512\nX qualquer coisa nova\n"},
		{"porta ilegivel", "p512\nn192.168.0.5:52341->203.0.113.9:abc\n"},
		{"lixo", "p512\nnisto nao e endereco\n"},
	}
	for _, c := range casos {
		if got := contaSocketsSessao(c.saida); got != 0 {
			t.Errorf("%s: contaSocketsSessao = %d, esperava 0", c.nome, got)
		}
	}
}

// O horario de inicio do processo do cliente e o que distingue "o cliente reiniciou e
// derrubou tudo" de "o log so rotacionou". Ler errado aqui encerra sessao viva.
func TestParseLstart(t *testing.T) {
	loc := time.FixedZone("BRT", -3*60*60)

	t.Run("formato do ps", func(t *testing.T) {
		got, ok := parseLstart("Wed Aug 20 08:14:33 2026", loc)
		if !ok {
			t.Fatal("nao interpretou a saida normal do ps")
		}
		want := time.Date(2026, time.August, 20, 8, 14, 33, 0, loc)
		if !got.Equal(want) {
			t.Errorf("parseLstart = %s, esperava %s", got, want)
		}
	})

	// Dia menor que 10 sai com espaco DUPLO ("Aug  5"), que e o formato do ps e nao
	// um erro de copia. Sem normalizar o espaco, todo dia 1 a 9 do mes falharia.
	t.Run("dia de um digito vem com espaco duplo", func(t *testing.T) {
		got, ok := parseLstart("Sat Aug  5 23:04:00 2028", loc)
		if !ok {
			t.Fatal("nao interpretou dia de um digito")
		}
		if got.Day() != 5 || got.Month() != time.August {
			t.Errorf("parseLstart = %s, esperava 5 de agosto", got)
		}
	})

	t.Run("quebra de linha do ps nao atrapalha", func(t *testing.T) {
		if _, ok := parseLstart("Wed Aug 20 08:14:33 2026\n", loc); !ok {
			t.Error("saida com \\n devia ser aceita")
		}
	})

	// Na duvida, false: quem nao sabe nao age.
	t.Run("saida ilegivel devolve false", func(t *testing.T) {
		for _, s := range []string{"", "   ", "sei la", "20/08/2026 08:14:33"} {
			if _, ok := parseLstart(s, loc); ok {
				t.Errorf("parseLstart(%q) devia falhar, mas aceitou", s)
			}
		}
	})
}

// PROVA DE CAMPO (20/08/2026): num MacBook em pt-BR, `ps -o lstart=` respondeu
//
//	qui 20 ago 17:40:23 2026
//
// com nomes em portugues e o dia ANTES do mes. Este teste fixa que o parser rejeita
// isso — de proposito. A solucao nao e ensinar idiomas ao parser (seriam todos os do
// mundo, e o formato tambem muda de ordem); e rodar o ps com LC_ALL=C, o que
// clientProcStartTime faz.
//
// Se alguem um dia remover aquele LC_ALL=C achando que e enfeite, o agente para de
// enxergar o restart do cliente em toda maquina que nao esteja em ingles — ou seja,
// em praticamente todo cliente nosso — e a sessao fica presa como fantasma. Este
// teste e o bilhete explicando isso.
func TestParseLstartRejeitaSaidaLocalizada(t *testing.T) {
	localizadas := []string{
		"qui 20 ago 17:40:23 2026", // pt-BR, visto em campo
		"jue 20 ago 17:40:23 2026", // es
		"gio 20 ago 17:40:23 2026", // it
	}
	for _, s := range localizadas {
		if _, ok := parseLstart(s, time.UTC); ok {
			t.Errorf("parseLstart(%q) aceitou saida localizada — o parser assume LC_ALL=C", s)
		}
	}

	// E a mesma máquina, com LC_ALL=C, responderia assim — que tem que passar.
	if _, ok := parseLstart("Thu Aug 20 17:40:23 2026", time.UTC); !ok {
		t.Error("parseLstart recusou a saida em C locale, que e a que o agente forca")
	}
}
