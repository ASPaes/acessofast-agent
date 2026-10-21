package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestMain aponta o hold para um diretorio temporario no pacote INTEIRO, nao so nos
// testes deste arquivo: todo teste que simula um login (peer_id, conexao longa) passa
// por marcaAutenticada, que grava o hold. Sem isto, rodar a suite numa maquina com o
// agente instalado escreveria em C:\ProgramData\AcessoFast e seguraria a senha dela.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "acessofast-hold")
	if err != nil {
		panic(err)
	}
	holdFile = filepath.Join(dir, "hold.until")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func limpaHold(t *testing.T) {
	t.Helper()
	os.Remove(holdFile)
	t.Cleanup(func() { os.Remove(holdFile) })
}

// O caso do teste de 21/10/2026: o tecnico entra e reinicia a maquina 14 segundos depois,
// antes do primeiro heartbeat. O hold tem que existir ja no login.
func TestLoginJaSeguraASenhaParaOReboot(t *testing.T) {
	limpaHold(t)
	tl := &tailer{open: make(map[string]time.Time)}

	tl.processLine("[info] #1 Connection opened from 192.168.35.111:53179.")
	if holdActive() {
		t.Fatal("conexao aberta mas sem login: tentativa recusada nao pode segurar a senha")
	}
	tl.processLine("[info] #1 peer_id 307871329")
	if !holdActive() {
		t.Fatal("login aceito: o hold devia existir para a senha sobreviver a um reboot agora")
	}
}

// Tentativa recusada (senha errada) nunca segura a senha: ela nao viu senha valida.
func TestTentativaRecusadaNaoSeguraASenha(t *testing.T) {
	limpaHold(t)
	tl := &tailer{open: make(map[string]time.Time)}

	tl.processLine("[info] #7 Connection opened from 189.4.111.147:12288.")
	tl.renovaHold() // heartbeat no meio da tentativa
	tl.processLine("[info] #7 Connection closed: Peer close")

	if holdActive() {
		t.Fatal("tentativa recusada segurou a senha: o boot seguinte deixaria de girar")
	}
}

// Sem acesso aberto o heartbeat nao renova (a carencia depois do fim nao conta).
func TestHoldSoRenovaComAcessoAberto(t *testing.T) {
	limpaHold(t)
	tl := &tailer{open: make(map[string]time.Time), autenticada: true}

	tl.renovaHold()
	if holdActive() {
		t.Fatal("sem conexao aberta o hold nao devia ser escrito")
	}
}

// O prazo fica holdAcessoValidade a frente, e a rotacao (senha nova) apaga o hold.
func TestHoldPrazoEApagaNaRotacao(t *testing.T) {
	limpaHold(t)
	tl := &tailer{open: map[string]time.Time{"1": time.Now()}, autenticada: true}

	antes := time.Now()
	tl.renovaHold()
	ate, err := time.Parse(time.RFC3339, readTrim(holdFile))
	if err != nil {
		t.Fatalf("hold ilegivel: %v", err)
	}
	if d := ate.Sub(antes); d < holdAcessoValidade-time.Second || d > holdAcessoValidade+time.Second {
		t.Fatalf("prazo do hold = %s a frente, queria %s", d, holdAcessoValidade)
	}

	apagaHold()
	if holdActive() {
		t.Fatal("depois de senha nova aplicada o hold nao pode continuar valendo")
	}
	apagaHold() // sem arquivo: nao pode falhar nem logar como erro
}

// Hold vencido (o reboot demorou mais que holdAcessoValidade) volta ao comportamento
// antigo: gira no boot.
func TestHoldVencidoNaoSegura(t *testing.T) {
	limpaHold(t)
	escreveHold(time.Now().Add(-time.Minute))
	if holdActive() {
		t.Fatal("hold vencido nao pode adiar a rotacao do boot")
	}
}

// A janela do agente precisa cobrir a do app do tecnico, que tenta voltar por 5 minutos
// contados da queda. Se alguem encurtar esperaVoltaAposBoot abaixo disso, a senha gira
// enquanto o app ainda esta voltando — o defeito que este arquivo corrige.
func TestEsperaDoAgenteCobreAJanelaDoApp(t *testing.T) {
	const janelaDoApp = 5 * time.Minute
	if esperaVoltaAposBoot <= janelaDoApp {
		t.Fatalf("esperaVoltaAposBoot = %s, precisa passar de %s", esperaVoltaAposBoot, janelaDoApp)
	}
	if holdAcessoValidade <= esperaVoltaAposBoot {
		t.Fatalf("holdAcessoValidade (%s) precisa passar de esperaVoltaAposBoot (%s)", holdAcessoValidade, esperaVoltaAposBoot)
	}
}
