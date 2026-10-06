package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// Contrato entre tools/sign-manifest (-produto app) e atualiza_app.go, como o do agente.
func TestManifestoCanonicoAppFormato(t *testing.T) {
	got := manifestoCanonicoApp("2026.10.01-875ebe8", "DEADBEEF")
	want := "acessofast-client:v1:2026.10.01-875ebe8:deadbeef"
	if got != want {
		t.Fatalf("formato do manifesto do app mudou:\n  got  %q\n  want %q", got, want)
	}
}

// A separacao de dominio e o ponto: assinatura de release do AGENTE nao pode valer como
// release do APP (nem o contrario), mesmo com a mesma chave e os mesmos version/sha256.
func TestAssinaturaDoAgenteNaoValeParaOApp(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	u := &updateInfo{Version: "2026.10.01-875ebe8", SHA256: "deadbeef"}
	agente := ed25519.Sign(priv, []byte(manifestoCanonico(u.Version, u.SHA256)))
	if ed25519.Verify(pub, []byte(manifestoCanonicoApp(u.Version, u.SHA256)), agente) {
		t.Fatal("assinatura do agente conferiu como assinatura do app")
	}
	app := ed25519.Sign(priv, []byte(manifestoCanonicoApp(u.Version, u.SHA256)))
	if ed25519.Verify(pub, []byte(manifestoCanonico(u.Version, u.SHA256)), app) {
		t.Fatal("assinatura do app conferiu como assinatura do agente")
	}
}

// Com a chave embutida, assinatura de outra chave e reprovada também no caminho do app.
func TestVerificaAssinaturaAppRejeitaOutraChave(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	u := &updateInfo{Version: "2026.10.01-875ebe8", SHA256: "deadbeef"}
	u.Signature = base64.StdEncoding.EncodeToString(
		ed25519.Sign(priv, []byte(manifestoCanonicoApp(u.Version, u.SHA256))))
	if err := verificaAssinaturaDe(u, manifestoCanonicoApp); err == nil {
		t.Fatal("aceitou assinatura de uma chave que nao e a embutida")
	}
}

func TestMotivoPularApp(t *testing.T) {
	ok := &updateInfo{Version: "2026.10.05-abc1234", URL: "https://x", SHA256: "aa", Signature: "s"}
	casos := []struct {
		nome       string
		u          *updateInfo
		atual      string
		aplicado   string
		tentativas int
		pula       bool
	}{
		{"legado recebe", ok, "legado", "", 0, false},
		{"versao anterior recebe", ok, "2026.10.01-875ebe8", "", 0, false},
		{"sem app nao instala", ok, "", "", 0, true},
		{"mesma versao", ok, "2026.10.05-abc1234", "", 0, true},
		{"ja instalada nesta execucao", ok, "legado", "2026.10.05-abc1234", 0, true},
		{"falhou demais", ok, "legado", "", updateMaxTries, true},
		{"manifesto incompleto", &updateInfo{Version: "v"}, "legado", "", 0, true},
		{"nil", nil, "legado", "", 0, true},
	}
	for _, c := range casos {
		got := motivoPularApp(c.u, c.atual, c.aplicado, c.tentativas)
		if (got != "") != c.pula {
			t.Errorf("%s: motivo=%q, esperava pular=%v", c.nome, got, c.pula)
		}
	}
}

func TestVersaoNaPasta(t *testing.T) {
	dir := t.TempDir()
	if v := versaoNaPasta(dir); v != appVersaoLegado {
		t.Fatalf("sem arquivo: %q, esperado legado", v)
	}
	escreve := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, appVersaoArquivo), []byte(s), 0644); err != nil {
			t.Fatal(err)
		}
	}
	escreve("2026.10.01-875ebe8\r\n")
	if v := versaoNaPasta(dir); v != "2026.10.01-875ebe8" {
		t.Fatalf("com arquivo: %q", v)
	}
	escreve("qualquer coisa com espaco")
	if v := versaoNaPasta(dir); v != appVersaoLegado {
		t.Fatalf("conteudo estranho virou versao: %q", v)
	}
	escreve("")
	if v := versaoNaPasta(dir); v != appVersaoLegado {
		t.Fatalf("arquivo vazio virou versao: %q", v)
	}
}

// Acesso remoto conta em qualquer porta, menos a do vinculo com o servidor e a web (o app
// fala com o painel por HTTPS o tempo todo; contar isso travaria a atualizacao do tecnico).
func TestPortaIgnoradaNoAcesso(t *testing.T) {
	for _, p := range []uint16{portaNatTest, portaRendezvous, 80, 443} {
		if !portaIgnoradaNoAcesso(p) {
			t.Errorf("porta %d deveria ser ignorada", p)
		}
	}
	// 21117 = relay, 21118/21119 = websocket, 50123 = conexao direta (P2P).
	for _, p := range []uint16{21117, 21118, 21119, 50123} {
		if portaIgnoradaNoAcesso(p) {
			t.Errorf("porta %d e de acesso remoto e foi ignorada", p)
		}
	}
}
