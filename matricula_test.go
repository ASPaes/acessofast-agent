package main

import (
	"encoding/json"
	"testing"
	"time"
)

// A cadencia do poll de matricula tem duas velocidades, e errar pra qualquer lado
// custa caro: rapido demais pra sempre foi o que gerou 146 mil chamadas/dia de
// maquinas nunca adotadas; lento demais na hora errada atrasa uma adocao real
// diante do operador.
func TestClaimPollDelay(t *testing.T) {
	casos := []struct {
		nome      string
		startedAt time.Time
		querIsso  time.Duration
	}{
		{"acabou de comecar", time.Now(), claimPollHot},
		{"dentro da janela quente", time.Now().Add(-30 * time.Minute), claimPollHot},
		{"na borda, ainda quente", time.Now().Add(-claimHotWindow + time.Minute), claimPollHot},
		{"passou da janela", time.Now().Add(-claimHotWindow - time.Minute), claimPollCold},
		{"esquecida ha dias", time.Now().Add(-72 * time.Hour), claimPollCold},
		// Sem ancora confiavel o certo e NAO atrasar uma adocao que pode estar
		// acontecendo agora: erra pro lado do operador, nao pro lado da economia.
		{"sem ancora (zero)", time.Time{}, claimPollHot},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if got := claimPollDelay(c.startedAt); got != c.querIsso {
				t.Fatalf("claimPollDelay = %v, queria %v", got, c.querIsso)
			}
		})
	}
}

// started_at vive no disco justamente pra sobreviver a restart do servico e ao
// re-registro do claim (que acontece de hora em hora, quando ele expira). Se o
// campo nao sobrevivesse ao round-trip do enroll.state, a maquina voltaria pra
// janela quente pra sempre — o bug que estamos consertando, de volta.
func TestEnrollStatePreservaStartedAt(t *testing.T) {
	orig := enrollState{Nonce: "n", Token: "t", StartedAt: time.Now().Add(-5 * time.Hour)}
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var volta enrollState
	if err := json.Unmarshal(raw, &volta); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !volta.StartedAt.Equal(orig.StartedAt) {
		t.Fatalf("started_at nao sobreviveu: %v != %v", volta.StartedAt, orig.StartedAt)
	}
	if got := claimPollDelay(volta.StartedAt); got != claimPollCold {
		t.Fatalf("apos round-trip, delay = %v, queria %v", got, claimPollCold)
	}
}

// enroll.state gravado por versao anterior do agente nao tem o campo. Precisa
// desserializar sem erro e cair no ramo que carimba a data (zero -> quente).
func TestEnrollStateAntigoSemStartedAt(t *testing.T) {
	var st enrollState
	if err := json.Unmarshal([]byte(`{"nonce":"n","token":"t"}`), &st); err != nil {
		t.Fatalf("estado antigo nao desserializou: %v", err)
	}
	if st.Nonce != "n" || st.Token != "t" {
		t.Fatalf("nonce/token corrompidos: %+v", st)
	}
	if !st.StartedAt.IsZero() {
		t.Fatalf("started_at deveria vir zerado, veio %v", st.StartedAt)
	}
	if got := claimPollDelay(st.StartedAt); got != claimPollHot {
		t.Fatalf("estado antigo deve entrar quente, veio %v", got)
	}
}
