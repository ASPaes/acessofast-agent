package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func servidorDeTeste() *servidor {
	s := novoServidor("http://exemplo.invalido", "chave-de-teste")
	s.tokens = map[string]string{
		"307871329": sha256Hex("token-bom"),
	}
	return s
}

// A transicao offline->online tem que ser reportada UMA vez, nao a cada ciclo:
// o agente reabre a conexao a cada ~55s, e se cada reabertura virasse mudanca a
// economia inteira ia embora — voltariamos a 1.500 chamadas/dia por maquina.
func TestMarcaVivaReportaSoNaTransicao(t *testing.T) {
	s := servidorDeTeste()

	if !s.marcaViva("307871329") {
		t.Fatal("a primeira conexao tem que contar como mudanca")
	}
	for i := 0; i < 20; i++ {
		if s.marcaViva("307871329") {
			t.Fatalf("reabertura %d virou mudanca; so a transicao pode contar", i)
		}
	}
}

// Silencio prolongado derruba a maquina — e uma vez so. Sem isso a varredura
// reenfileiraria a mesma queda a cada 10 segundos, para sempre.
func TestVarreDerrubaUmaVezSo(t *testing.T) {
	s := servidorDeTeste()
	s.marcaViva("307871329")

	if cairam := s.varre(); len(cairam) != 0 {
		t.Fatalf("maquina recem-vista nao pode cair: %v", cairam)
	}

	s.mu.Lock()
	s.maquinas["307871329"].ultimoSinal = time.Now().Add(-prazoSilencio - time.Second)
	s.mu.Unlock()

	cairam := s.varre()
	if len(cairam) != 1 || cairam[0] != "307871329" {
		t.Fatalf("esperava a maquina cair, veio %v", cairam)
	}
	if segunda := s.varre(); len(segunda) != 0 {
		t.Fatalf("a queda foi reportada duas vezes: %v", segunda)
	}
}

// Oscilacao dentro da janela de descarga vale pelo ULTIMO estado. Uma maquina
// com rede ruim nao pode encher o painel (e o historico) de pisca-pisca.
func TestEnfileiraGuardaOUltimoEstado(t *testing.T) {
	s := servidorDeTeste()
	s.enfileira("307871329", true)
	s.enfileira("307871329", false)
	s.enfileira("307871329", true)

	s.pendenteMu.Lock()
	defer s.pendenteMu.Unlock()
	if len(s.pendente) != 1 {
		t.Fatalf("esperava 1 entrada na fila, veio %d", len(s.pendente))
	}
	if !s.pendente["307871329"].Online {
		t.Fatal("a fila guardou um estado antigo em vez do ultimo")
	}
}

func TestTokenConfere(t *testing.T) {
	s := servidorDeTeste()
	casos := []struct {
		nome, id, token string
		querPassar      bool
	}{
		{"token certo", "307871329", "token-bom", true},
		{"token errado", "307871329", "token-ruim", false},
		{"maquina desconhecida", "999999999", "token-bom", false},
		{"token vazio", "307871329", "", false},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := s.tokenConfere(c.id, c.token); got != c.querPassar {
				t.Fatalf("tokenConfere = %v, queria %v", got, c.querPassar)
			}
		})
	}
}

// O endpoint precisa recusar entrada malformada ANTES de tocar no estado — senao
// qualquer um enche o mapa de maquinas inventadas so mandando json.
func TestHandlerRecusaEntradaInvalida(t *testing.T) {
	s := servidorDeTeste()
	casos := []struct {
		nome   string
		corpo  string
		status int
	}{
		{"json quebrado", `{`, http.StatusBadRequest},
		{"id fora do formato", `{"rustdesk_id":"abc","agent_token":"token-bom"}`, http.StatusBadRequest},
		{"sem token", `{"rustdesk_id":"307871329"}`, http.StatusBadRequest},
		{"token invalido", `{"rustdesk_id":"307871329","agent_token":"x"}`, http.StatusUnauthorized},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/v1/presenca", bytes.NewBufferString(c.corpo))
			w := httptest.NewRecorder()
			s.handlePresenca(w, req)
			if w.Code != c.status {
				t.Fatalf("status = %d, queria %d", w.Code, c.status)
			}
		})
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.maquinas) != 0 {
		t.Fatalf("entrada invalida sujou o estado: %v", s.maquinas)
	}
}

// Quando o cliente vai embora no meio do long-poll, o handler tem que RETORNAR
// (liberando a goroutine) sem marcar offline ali mesmo. Quem decide queda e a
// varredura, com o prazo de silencio — uma fonte so de verdade.
func TestHandlerSoltaQuandoOClienteSome(t *testing.T) {
	s := servidorDeTeste()
	req := httptest.NewRequest("POST", "/v1/presenca",
		bytes.NewBufferString(`{"rustdesk_id":"307871329","agent_token":"token-bom"}`))
	ctx, cancela := contextComCancelamento(req)
	req = req.WithContext(ctx)

	pronto := make(chan struct{})
	go func() {
		s.handlePresenca(httptest.NewRecorder(), req)
		close(pronto)
	}()

	time.Sleep(50 * time.Millisecond)
	cancela() // o agente sumiu

	select {
	case <-pronto:
	case <-time.After(2 * time.Second):
		t.Fatal("o handler ficou preso depois de o cliente sumir")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if m := s.maquinas["307871329"]; m == nil || !m.online {
		t.Fatal("desconexao nao pode marcar offline na hora — isso e papel da varredura")
	}
}

// A descarga nao pode PERDER mudanca quando o Supabase esta fora: perder uma
// transicao deixa o painel mentindo ate a proxima, que pode nao vir tao cedo.
func TestDescargaDevolveAFilaQuandoFalha(t *testing.T) {
	s := servidorDeTeste() // aponta para host invalido: a chamada vai falhar
	s.enfileira("307871329", true)

	s.descarrega(contextoDeTeste())

	s.pendenteMu.Lock()
	defer s.pendenteMu.Unlock()
	if len(s.pendente) != 1 {
		t.Fatalf("a mudanca sumiu depois da falha: fila com %d", len(s.pendente))
	}
}

// O lote enviado precisa bater com o formato que a RPC aplicar_presenca espera.
func TestFormatoDoLote(t *testing.T) {
	m := mudanca{RustdeskID: "307871329", Online: true, Em: time.Now().UTC().Format(time.RFC3339)}
	bruto, err := json.Marshal([]mudanca{m})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var volta []map[string]any
	if err := json.Unmarshal(bruto, &volta); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, campo := range []string{"rustdesk_id", "online", "em"} {
		if _, tem := volta[0][campo]; !tem {
			t.Fatalf("o lote nao tem o campo %q que a RPC le", campo)
		}
	}
}

// Auxiliares dos testes acima.

func contextComCancelamento(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithCancel(r.Context())
}

func contextoDeTeste() context.Context {
	ctx, cancela := context.WithTimeout(context.Background(), 5*time.Second)
	_ = cancela // o teste termina antes; o timeout e a rede de seguranca
	return ctx
}
