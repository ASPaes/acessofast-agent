package main

import (
	"testing"
	"time"
)

// Reseta o estado global entre casos: os testes rodam no mesmo processo e o
// controle de repeticao e um par de variaveis de pacote.
func limpaEstadoAviso() {
	avisoMu.Lock()
	avisoUltimo = time.Time{}
	avisoUltimoT = ""
	avisoMu.Unlock()
}

// Aviso nulo ou sem texto nao pode chegar ao msg.exe: o servidor pode devolver
// o campo vazio (falha na busca e fail-open), e disparar um comando com texto
// em branco poria uma janela vazia na cara do operador.
func TestMostraAvisoIgnoraVazio(t *testing.T) {
	limpaEstadoAviso()

	mostraAviso(nil)
	mostraAviso(&avisoServidor{Titulo: "t", Mensagem: "   "})

	avisoMu.Lock()
	defer avisoMu.Unlock()
	if !avisoUltimo.IsZero() {
		t.Fatal("aviso vazio marcou o estado; deveria ter sido descartado antes")
	}
}

// O mesmo aviso repetido dentro da janela nao pode empilhar janelas. O servidor
// ja deduplica enquanto o aviso esta pendente, mas o operador que reconecta ao
// longo do dia geraria avisos novos — e sem este freio viraria uma fila de
// pop-ups, que e o caminho mais curto para o aviso ser ignorado.
func TestMostraAvisoSuprimeRepetido(t *testing.T) {
	limpaEstadoAviso()
	a := &avisoServidor{Titulo: "AcessoFast desatualizado", Mensagem: "mensagem"}

	mostraAviso(a)
	avisoMu.Lock()
	primeiro := avisoUltimo
	avisoMu.Unlock()
	if primeiro.IsZero() {
		t.Fatal("o primeiro aviso nao foi registrado")
	}

	// Segunda chamada imediata: suprimida, e NAO pode reposicionar o relogio —
	// senao uma sequencia de repeticoes empurraria a janela indefinidamente.
	mostraAviso(a)
	avisoMu.Lock()
	defer avisoMu.Unlock()
	if !avisoUltimo.Equal(primeiro) {
		t.Fatal("a repeticao suprimida mexeu no relogio do ultimo aviso")
	}
}

// Aviso com titulo DIFERENTE passa mesmo dentro da janela: e outro assunto, e
// silenciar um recado novo porque outro apareceu ha pouco esconde informacao.
func TestMostraAvisoDeixaPassarOutroAssunto(t *testing.T) {
	limpaEstadoAviso()

	mostraAviso(&avisoServidor{Titulo: "assunto A", Mensagem: "m"})
	mostraAviso(&avisoServidor{Titulo: "assunto B", Mensagem: "m"})

	avisoMu.Lock()
	defer avisoMu.Unlock()
	if avisoUltimoT != "assunto B" {
		t.Fatalf("o segundo assunto foi suprimido: ultimo = %q", avisoUltimoT)
	}
}

// Passada a janela, o mesmo aviso volta a aparecer — o problema continua la, e
// um lembrete a cada 15 min e o que mantem a maquina na lista de pendencias do
// operador sem virar ruido.
func TestMostraAvisoVoltaDepoisDaJanela(t *testing.T) {
	limpaEstadoAviso()
	a := &avisoServidor{Titulo: "mesmo assunto", Mensagem: "m"}

	mostraAviso(a)
	avisoMu.Lock()
	avisoUltimo = time.Now().Add(-avisoIntervaloMin - time.Minute)
	antigo := avisoUltimo
	avisoMu.Unlock()

	mostraAviso(a)
	avisoMu.Lock()
	defer avisoMu.Unlock()
	if avisoUltimo.Equal(antigo) {
		t.Fatal("o aviso nao voltou depois da janela de supressao")
	}
}
