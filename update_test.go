package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

// O formato do manifesto e um contrato entre DOIS programas: tools/sign-manifest
// assina esta string, update.go a reconstroi. Se um lado mudar sozinho, nenhum
// update passa a ser aceito — falha segura, mas silenciosa deste lado. O teste
// congela o formato pra que a mudanca quebre aqui, e nao em producao.
func TestManifestoCanonicoFormato(t *testing.T) {
	got := manifestoCanonico("2026.08.12-abc1234", "DEADBEEF")
	want := "acessofast-agent:v1:2026.08.12-abc1234:deadbeef"
	if got != want {
		t.Fatalf("formato do manifesto mudou:\n  got  %q\n  want %q", got, want)
	}
}

// O hash pode chegar em maiuscula ou minuscula dependendo de quem gerou; a string
// assinada tem que ser a mesma nos dois casos, senao a assinatura so valeria pra
// uma das grafias.
func TestManifestoCanonicoNormalizaHash(t *testing.T) {
	if manifestoCanonico("v", "ABC") != manifestoCanonico("v", "abc") {
		t.Fatal("manifesto sensivel a maiuscula/minuscula do sha256")
	}
}

func TestVerificaAssinaturaRejeita(t *testing.T) {
	// Assinatura sintaticamente valida mas gerada por OUTRA chave: e o caso do
	// servidor comprometido tentando empurrar um binario proprio.
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	u := &updateInfo{Version: "2026.08.12-abc1234", SHA256: "deadbeef"}
	u.Signature = base64.StdEncoding.EncodeToString(
		ed25519.Sign(priv, []byte(manifestoCanonico(u.Version, u.SHA256))))

	if err := verificaAssinatura(u); err == nil {
		t.Fatal("aceitou assinatura de chave desconhecida")
	}

	for _, caso := range []struct{ nome, sig string }{
		{"vazia", ""},
		{"nao-base64", "!!!nao-base64!!!"},
		{"tamanho errado", base64.StdEncoding.EncodeToString([]byte("curta"))},
	} {
		u.Signature = caso.sig
		if err := verificaAssinatura(u); err == nil {
			t.Fatalf("aceitou assinatura %s", caso.nome)
		}
	}
}

// Teste de interoperabilidade de verdade: assina com a MESMA chave privada que o
// CI usa e confere contra a chave publica REAL embutida em update.go. E o unico
// teste que provaria um par de chaves trocado.
//
// Pulado quando a privada nao esta no ambiente — em CI e na maquina de quem nao
// tem o secret. Nao e o teste que guarda o segredo; e o segredo que decide se ele
// roda.
func TestAssinaturaDoCIEAceitaPelaChaveEmbutida(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("AGENT_UPDATE_SIGNING_KEY"))
	if raw == "" {
		t.Skip("AGENT_UPDATE_SIGNING_KEY ausente — pulando o teste de interoperabilidade")
	}
	priv, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		t.Fatalf("chave privada invalida (%d bytes)", len(priv))
	}

	u := &updateInfo{Version: "2026.08.12-abc1234", SHA256: "DEADBEEF"}
	u.Signature = base64.StdEncoding.EncodeToString(
		ed25519.Sign(ed25519.PrivateKey(priv), []byte(manifestoCanonico(u.Version, u.SHA256))))

	if err := verificaAssinatura(u); err != nil {
		t.Fatalf("a chave publica embutida NAO aceita a assinatura do CI: %v\n"+
			"as duas metades do par estao trocadas — nenhum update seria aplicado", err)
	}
}

// A guarda que estes casos cobrem nasceu de um incidente real (2026-08-15, primeiro
// auto-update em maquina de cliente): entre a troca do binario e o restart agendado
// o processo vivo ainda e o binario ANTIGO, entao o agente segue se declarando na
// versao velha e o servidor reoferece o mesmo update a cada 60s. Sem a guarda o
// agente rebaixava ~5,7 MB e falhava no rename com "Access is denied" — o .old que
// ele tentaria sobrescrever e a imagem do proprio processo em execucao.
func TestMotivoPular(t *testing.T) {
	completo := func(v string) *updateInfo {
		return &updateInfo{Version: v, URL: "https://x/y.exe", SHA256: "abc", Signature: "sig"}
	}

	casos := []struct {
		nome        string
		u           *updateInfo
		versaoAtual string
		jaAplicado  string
		tentativas  int
		querPular   bool
	}{
		{"aplica quando ha versao nova", completo("v2"), "v1", "", 0, false},
		{"nil nao aplica", nil, "v1", "", 0, true},
		{"manifesto sem assinatura nao aplica",
			&updateInfo{Version: "v2", URL: "https://x/y.exe", SHA256: "abc"}, "v1", "", 0, true},
		{"manifesto sem sha nao aplica",
			&updateInfo{Version: "v2", URL: "https://x/y.exe", Signature: "sig"}, "v1", "", 0, true},
		{"build local nunca se auto-atualiza", completo("v2"), "dev", "", 0, true},
		{"ja estamos na versao oferecida", completo("v2"), "v2", "", 0, true},
		// O caso do incidente: versao diferente da atual (o processo vivo e o
		// binario velho), mas ja trocada no disco. Sem esta guarda, refaria tudo.
		{"ja trocada no disco, so falta reiniciar", completo("v2"), "v1", "v2", 0, true},
		{"outra versao ainda pode ser aplicada", completo("v3"), "v1", "v2", 0, false},
		{"desiste apos o teto de tentativas", completo("v2"), "v1", "", updateMaxTries, true},
		{"ainda tenta abaixo do teto", completo("v2"), "v1", "", updateMaxTries - 1, false},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			motivo := motivoPular(c.u, c.versaoAtual, c.jaAplicado, c.tentativas)
			if pulou := motivo != ""; pulou != c.querPular {
				t.Fatalf("querPular=%v, mas motivo=%q", c.querPular, motivo)
			}
		})
	}
}
