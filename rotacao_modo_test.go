package main

import (
	"os"
	"path/filepath"
	"testing"
)

// usaModoTemporario aponta o cache do modo para um diretorio do proprio teste e zera o
// estado em memoria. Sem isto o teste gravaria em C:\ProgramData\AcessoFast da maquina
// de quem roda — e numa maquina com agente instalado, mudaria o modo de verdade.
func usaModoTemporario(t *testing.T) string {
	t.Helper()
	orig := rotacaoModoFile
	rotacaoModoFile = filepath.Join(t.TempDir(), "rotacao.modo")
	esqueceModoEmMemoria()
	t.Cleanup(func() {
		rotacaoModoFile = orig
		esqueceModoEmMemoria()
	})
	return rotacaoModoFile
}

// esqueceModoEmMemoria simula o processo reiniciando: some a memoria, fica o disco.
func esqueceModoEmMemoria() {
	rotacaoMu.Lock()
	rotacaoModo = ""
	rotacaoMu.Unlock()
}

// A tabela do plano, gatilho a gatilho. Cada linha e uma decisao que foi tomada de
// proposito, e mudar qualquer uma delas precisa ser uma decisao nova — nao um efeito
// colateral de refatoracao.
func TestDecideRotacaoTabela(t *testing.T) {
	casos := []struct {
		modo string
		g    gatilhoRotacao
		quer bool
	}{
		{modoSession, gatilhoFimSessao, true},
		{modoSession, gatilhoBoot, true},
		{modoSession, gatilhoRestartCliente, true},

		{modoInstallOnly, gatilhoFimSessao, false},
		{modoInstallOnly, gatilhoBoot, false},
		// recuperacao de divergencia: o cliente subiu lendo a senha do disco
		{modoInstallOnly, gatilhoRestartCliente, true},

		{modoOff, gatilhoFimSessao, false},
		{modoOff, gatilhoBoot, false},
		{modoOff, gatilhoRestartCliente, false},
	}
	for _, c := range casos {
		if got := decideRotacao(c.modo, c.g); got != c.quer {
			t.Errorf("decideRotacao(%s, %s) = %v, queria %v", c.modo, c.g, got, c.quer)
		}
	}
}

// shadow existe para validar a cascata em campo sem mudar nada. Se ele divergir de
// session em QUALQUER gatilho, deixou de ser shadow e virou um quinto modo escondido.
func TestShadowNuncaMudaComportamento(t *testing.T) {
	for _, g := range []gatilhoRotacao{gatilhoFimSessao, gatilhoBoot, gatilhoRestartCliente} {
		if decideRotacao(modoShadow, g) != decideRotacao(modoSession, g) {
			t.Errorf("shadow divergiu de session no gatilho %s", g)
		}
	}
}

// Valor que o agente nao reconhece tem de GIRAR. O pior caso de um defeito no modo e
// o comportamento de antes — nunca uma maquina que parou de girar sem ninguem pedir.
// Os casos sao os erros plausiveis: vazio, caixa errada, hifen no lugar do sublinhado.
func TestModoDesconhecidoGira(t *testing.T) {
	for _, m := range []string{"", "OFF", "Install_Only", "install-only", "desliga"} {
		if modoValido(m) {
			t.Errorf("modoValido(%q) = true; nao e um dos quatro modos", m)
		}
		for _, g := range []gatilhoRotacao{gatilhoFimSessao, gatilhoBoot, gatilhoRestartCliente} {
			if !decideRotacao(m, g) {
				t.Errorf("modo desconhecido %q deixou de girar em %s", m, g)
			}
		}
	}
}

// Sem arquivo e sem servidor — maquina recem-atualizada, antes do primeiro presence —
// o agente gira como sempre girou.
func TestModoSemCacheEhSession(t *testing.T) {
	usaModoTemporario(t)
	if got := modoRotacao(); got != modoSession {
		t.Fatalf("sem cache, modoRotacao() = %q, queria %q", got, modoSession)
	}
}

// O modo recebido sobrevive ao reinicio do agente. E isso que faz o BOOT respeitar o
// modo: o boot roda antes do primeiro presence, e so tem o disco para consultar.
func TestModoPersisteEntreReinicios(t *testing.T) {
	arq := usaModoTemporario(t)

	gravaModoRotacao(modoInstallOnly)
	esqueceModoEmMemoria() // o agente reiniciou

	if got := modoRotacao(); got != modoInstallOnly {
		t.Fatalf("depois do reinicio, modoRotacao() = %q, queria %q", got, modoInstallOnly)
	}
	b, err := os.ReadFile(arq)
	if err != nil {
		t.Fatalf("o modo nao foi gravado em disco: %v", err)
	}
	if string(b) != modoInstallOnly {
		t.Fatalf("disco tem %q, queria %q", string(b), modoInstallOnly)
	}
}

// Valor invalido vindo do servidor nao derruba o modo em vigor. Um campo corrompido no
// transporte nao pode ser o que faz uma maquina em install_only voltar a girar.
func TestModoInvalidoNaoSobrescreve(t *testing.T) {
	usaModoTemporario(t)

	gravaModoRotacao(modoInstallOnly)
	gravaModoRotacao("xpto")
	gravaModoRotacao("")

	if got := modoRotacao(); got != modoInstallOnly {
		t.Fatalf("valor invalido mexeu no modo: %q, queria %q", got, modoInstallOnly)
	}
}

// O caminho de volta tem de funcionar: reverter o canario e so trocar a linha no banco
// para session, e o agente precisa obedecer. Sem isso o rollback do plano nao existe.
func TestModoVoltaParaSession(t *testing.T) {
	usaModoTemporario(t)

	gravaModoRotacao(modoInstallOnly)
	gravaModoRotacao(modoSession)
	esqueceModoEmMemoria()

	if got := modoRotacao(); got != modoSession {
		t.Fatalf("o rollback nao persistiu: %q, queria %q", got, modoSession)
	}
}

// Arquivo adulterado ou corrompido cai no padrao, e nao num modo que ninguem pediu.
func TestCacheCorrompidoCaiNoPadrao(t *testing.T) {
	arq := usaModoTemporario(t)
	if err := os.WriteFile(arq, []byte("desliga-tudo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := modoRotacao(); got != modoSession {
		t.Fatalf("cache corrompido virou %q, queria %q", got, modoSession)
	}
}
