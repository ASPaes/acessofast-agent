package main

import (
	"testing"
	"time"
)

// O caso que motivou o guard (007BOMBONIERE03, 25/08/2026): o cliente loga
// "#N Connection opened" no accept do TCP, ANTES de validar a senha. A tentativa
// RECUSADA produzia opened + closed identicos aos de uma sessao real, o agente
// rotacionava 60s depois e matava justamente a senha que o painel tinha acabado de
// entregar — a tentativa seguinte falhava pelo mesmo motivo, em laco.
func TestTentativaRecusadaNaoAutenticaSessao(t *testing.T) {
	tl := &tailer{open: make(map[string]time.Time)}

	tl.processLine("[info] #7 Connection opened from 189.4.111.147:12288.")
	tl.processLine("[info] #7 Connection closed: Peer close")

	if tl.autenticada {
		t.Fatal("opened+closed em segundos e senha recusada: a sessao NAO devia contar como autenticada")
	}
	if len(tl.open) != 0 {
		t.Fatalf("o closed devia ter soltado o #7, restaram %d", len(tl.open))
	}
	if tl.graceUntil.IsZero() {
		t.Error("a carencia continua sendo armada — o que muda e so a rotacao no fim dela")
	}
}

// peer_id so sai depois do login aceito: e a prova direta de sessao de verdade.
func TestPeerIdAutenticaSessao(t *testing.T) {
	tl := &tailer{open: make(map[string]time.Time)}

	tl.processLine("[info] #7 Connection opened from 189.4.111.147:12288.")
	if tl.autenticada {
		t.Fatal("so o accept do TCP nao autentica")
	}

	tl.processLine("[info] #7 peer_id 126723850")
	if !tl.autenticada {
		t.Fatal("peer_id vem depois do login: devia autenticar a sessao")
	}
	if tl.controllerID != "126723850" {
		t.Errorf("o controlador devia continuar sendo aprendido, veio %q", tl.controllerID)
	}
}

// Rede de protecao para o cliente que nao emite peer_id: uma conexao que passou do
// limiar nao foi senha recusada (essa fecha em segundos).
func TestDuracaoAutenticaSemPeerId(t *testing.T) {
	tl := &tailer{open: make(map[string]time.Time)}

	tl.processLine("[info] #7 Connection opened from 189.4.111.147:12288.")
	// Recua o inicio para alem do limiar, sem esperar de verdade no teste.
	tl.open["7"] = time.Now().Add(-duracaoMinAutenticada - time.Second)

	tl.processLine("[info] #7 Connection closed: Peer close")
	if !tl.autenticada {
		t.Fatalf("conexao aberta por mais de %s devia autenticar mesmo sem peer_id", duracaoMinAutenticada)
	}
}

// autenticaPorTempo cobre os fins que nao passam pelo "closed" (fantasma, corte,
// expiracao): a conexao ainda esta em t.open quando a decisao de rotacionar acontece.
func TestAutenticaPorTempoCobreConexaoAindaAberta(t *testing.T) {
	tl := &tailer{open: make(map[string]time.Time)}

	tl.open["7"] = time.Now().Add(-time.Second)
	tl.autenticaPorTempo()
	if tl.autenticada {
		t.Fatal("conexao recem-aberta nao devia autenticar")
	}

	tl.open["7"] = time.Now().Add(-duracaoMinAutenticada - time.Second)
	tl.autenticaPorTempo()
	if !tl.autenticada {
		t.Fatal("conexao aberta ha mais que o limiar devia autenticar")
	}
}

// A marca vale pela sessao inteira, inclusive atravessando a carencia (reconexao por
// blip e a MESMA sessao), e e limpa no fim REAL — sem vazar para a sessao seguinte.
func TestMarcaSobreviveACarenciaEMorreNoFim(t *testing.T) {
	tl := &tailer{open: make(map[string]time.Time)}

	tl.processLine("[info] #7 Connection opened from 189.4.111.147:12288.")
	tl.processLine("[info] #7 peer_id 126723850")
	tl.processLine("[info] #7 Connection closed: Peer close")
	if !tl.autenticada {
		t.Fatal("a marca devia sobreviver ao closed enquanto a carencia esta armada")
	}

	// Reconexao dentro da carencia: mesma sessao, a marca continua valendo.
	tl.processLine("[info] #8 Connection opened from 189.4.111.147:12290.")
	if !tl.graceUntil.IsZero() {
		t.Error("a reconexao devia ter cancelado a carencia")
	}
	if !tl.autenticada {
		t.Fatal("reconexao e a mesma sessao: a marca nao devia se perder")
	}

	// Fim real: rotaciona e zera a marca.
	tl.processLine("[info] #8 Connection closed: Peer close")
	tl.rotacionarSeAutenticada("teste")
	if tl.autenticada {
		t.Fatal("o fim real devia limpar a marca, senao ela vaza pra proxima sessao")
	}
}

// O gate do cliente (25/08/2026): esperaCliente nao pode travar o startup do agente
// quando o servico nunca sobe. Com o limite ja vencido ela decide na hora, e a decisao
// e exatamente a leitura direta do servico — sem laco, sem sleep.
func TestEsperaClienteNaoTravaComLimiteVencido(t *testing.T) {
	inicio := time.Now()
	got := esperaCliente(0)
	if d := time.Since(inicio); d > 2*time.Second {
		t.Fatalf("com limite vencido devia decidir na hora, levou %s", d)
	}
	if got != clienteVivo() {
		t.Fatal("com limite vencido o resultado deve ser a leitura direta do servico")
	}
}

// Autocura (26/08/2026): o agente nao pode agir no primeiro tick em que ve o servico
// fora do ar — o corte do free para e sobe o cliente de proposito, e uma atualizacao
// tambem passa por ali. So depois da tolerancia a subida e legitima.
func TestAutocuraEsperaToleranciaAntesDeSubir(t *testing.T) {
	if clienteVivo() {
		t.Skip("servico do cliente rodando nesta maquina: o caminho da autocura nao e exercitado")
	}
	tl := &tailer{open: make(map[string]time.Time)}

	tl.checkClienteParado()
	if tl.clienteParadoDesde.IsZero() {
		t.Fatal("o primeiro tick devia armar o relogio do servico parado")
	}
	if !tl.ultimaSubidaCliente.IsZero() {
		t.Fatal("o primeiro tick NAO pode tentar subir — a tolerancia existe pra isso")
	}

	// Recua o relogio para alem da tolerancia: agora a subida e devida.
	tl.clienteParadoDesde = time.Now().Add(-clienteParadoTolerancia - time.Second)
	tl.checkClienteParado()
	if tl.ultimaSubidaCliente.IsZero() {
		t.Fatal("passada a tolerancia, devia ter tentado subir o servico")
	}

	// E a tentativa seguinte tem que respeitar o intervalo, senao vira laco de start.
	marca := tl.ultimaSubidaCliente
	tl.checkClienteParado()
	if !tl.ultimaSubidaCliente.Equal(marca) {
		t.Fatal("tentativa nova dentro do intervalo: viraria laco de start a cada tick")
	}
}
