package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// A regra tem de bater com a da edge definir-senha-dispositivo. Os casos de recusa sao
// os que importam: a senha vai para uma linha de comando.
func TestSenhaAceitavel(t *testing.T) {
	casos := []struct {
		pw   string
		quer bool
	}{
		{"Acesso2026", true},
		{"a1b2c3d4", true},
		{"Senha-Forte_9.x?", true},
		{"abc12", false},                  // curta
		{strings.Repeat("a1", 33), false}, // 66 > 64
		{"somenteletras", false},          // sem digito
		{"12345678", false},               // sem letra
		{"com espaco 1", false},           // espaco
		{`aspas"1234`, false},             // aspas quebram a linha de comando
		{`barra\1234a`, false},            // barra invertida idem
		{"acentuação1", false},            // fora do ASCII
		{"linha\nnova1a", false},          // controle
		{"", false},
	}
	for _, c := range casos {
		if got := senhaAceitavel(c.pw); got != c.quer {
			t.Errorf("senhaAceitavel(%q) = %v, quer %v", c.pw, got, c.quer)
		}
	}
}

func TestPedidoJaAplicado(t *testing.T) {
	const id = "0b6f1f3e-6a4b-4d53-9a51-5d7c6b0e2a11"
	if !pedidoJaAplicado("senha", id, id) {
		t.Error("pendencia do mesmo pedido: deveria dizer que ja aplicou")
	}
	if pedidoJaAplicado("", id, id) {
		t.Error("sem pendencia o painel ja confirmou; um presence com o mesmo pedido e reentrega e deve aplicar")
	}
	if pedidoJaAplicado("senha", "", id) {
		t.Error("pendencia de rotacao (sem pedido) nao e este pedido")
	}
	if pedidoJaAplicado("senha", "outro", id) {
		t.Error("pendencia de outro pedido nao e este pedido")
	}
}

// O pior defeito possivel deste arquivo: a senha pedida em texto claro no agent.log.
func TestCorpoParaLogOmiteSenha(t *testing.T) {
	const pw = "Acesso2026xyz"
	body, _ := json.Marshal(map[string]any{
		"ok": true, "action": "presence", "rotacao": "install_only",
		"senha": map[string]string{"pedido_id": "0b6f1f3e-6a4b-4d53-9a51-5d7c6b0e2a11", "senha": pw},
	})
	got := corpoParaLog(body)
	if strings.Contains(got, pw) {
		t.Fatalf("senha vazou no log: %s", got)
	}
	if !strings.Contains(got, "[omitida]") || !strings.Contains(got, "install_only") {
		t.Errorf("deveria omitir so a senha e manter o resto: %s", got)
	}

	// Corpo ilegivel com cara de senha: some inteiro.
	if got := corpoParaLog([]byte(`{"senha":{"senha":"` + pw + `"`)); strings.Contains(got, pw) {
		t.Fatalf("senha vazou de corpo truncado: %s", got)
	}

	// Sem senha: igual ao que era logado antes.
	semSenha := []byte(`{"ok":true,"action":"presence"}`)
	if got := corpoParaLog(semSenha); got != string(semSenha) {
		t.Errorf("corpo sem senha foi alterado: %s", got)
	}
}

func TestCorpoParaLogTrunca(t *testing.T) {
	got := corpoParaLog([]byte(strings.Repeat("x", 1000)))
	if !strings.HasSuffix(got, "…") || len(got) > 400+len("…") {
		t.Errorf("deveria truncar em 400: len=%d", len(got))
	}
}

func TestPedidoArquivo(t *testing.T) {
	orig := pedidoFile
	pedidoFile = filepath.Join(t.TempDir(), "sub", "senha.pedido")
	defer func() { pedidoFile = orig }()

	if readPedido() != "" {
		t.Fatal("sem arquivo deveria ler vazio")
	}
	const id = "0b6f1f3e-6a4b-4d53-9a51-5d7c6b0e2a11"
	if err := writePedido(id); err != nil {
		t.Fatal(err)
	}
	if readPedido() != id {
		t.Fatalf("leu %q, quer %q", readPedido(), id)
	}
	clearPedido()
	if readPedido() != "" {
		t.Fatal("clearPedido nao apagou")
	}
}

// O pedido_id so vai no reporte quando existe: rotacao comum continua mandando
// exatamente o corpo de antes, e o rotate-device-secret antigo nao ve campo novo.
func TestPayloadRotacao(t *testing.T) {
	var m map[string]string
	_ = json.Unmarshal(payloadRotacao("pw", ""), &m)
	if _, ok := m["pedido_id"]; ok {
		t.Error("rotacao sem pedido nao deveria mandar pedido_id")
	}
	_ = json.Unmarshal(payloadRotacao("pw", "abc"), &m)
	if m["pedido_id"] != "abc" || m["password"] != "pw" {
		t.Errorf("payload com pedido errado: %v", m)
	}
}
