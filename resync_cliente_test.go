package main

import (
	"testing"
	"time"
)

func TestDecideSubidaDoCliente(t *testing.T) {
	t0 := time.Date(2026, 9, 24, 16, 0, 0, 0, time.UTC)
	t1 := t0.Add(5 * time.Minute)
	zero := time.Time{}

	casos := []struct {
		nome                          string
		noAr                          bool
		inicio, anterior              time.Time
		ausenteAntes                  bool
		subiu, apareceu, ausenteAgora bool
	}{
		// O caso que faltava: e o unico que distingue esta regra da anterior.
		{"apareceu depois de ausente", true, t1, zero, true, true, true, false},

		// Primeira leitura do agente com o cliente JA no ar: nao rotaciona aqui, senao
		// gira duas vezes na subida (o rotateOnBoot ja gira).
		{"primeira leitura, cliente no ar", true, t0, zero, false, false, false, false},

		{"reiniciou", true, t1, t0, false, true, false, false},
		{"mesmo processo", true, t0, t0, false, false, false, false},

		// Fora do ar: o retorno de ausencia e o que arma o caso APARECEU no ciclo
		// seguinte. Sem ele a regra nunca dispara.
		{"fora do ar", false, zero, t0, false, false, false, true},
		{"fora do ar, seguia ausente", false, zero, zero, true, false, false, true},

		// Horario ANTERIOR ao conhecido: relogio que andou pra tras nao e restart. O
		// After() e estrito de proposito.
		{"inicio mais antigo", true, t0, t1, false, false, false, false},
	}

	for _, c := range casos {
		subiu, apareceu, ausente := decideSubidaDoCliente(c.noAr, c.inicio, c.anterior, c.ausenteAntes)
		if subiu != c.subiu || apareceu != c.apareceu || ausente != c.ausenteAgora {
			t.Errorf("%s: subiu=%v apareceu=%v ausente=%v; esperado %v/%v/%v",
				c.nome, subiu, apareceu, ausente, c.subiu, c.apareceu, c.ausenteAgora)
		}
	}
}

// A sequencia real do primeiro Mac: o cliente nao estava no ar, o agente leu varias
// vezes sem achar, e horas depois o LaunchAgent foi carregado. A senha tinha de ser
// publicada nesse instante.
func TestSequenciaDoPrimeiroMac(t *testing.T) {
	agora := time.Date(2026, 9, 24, 16, 54, 0, 0, time.UTC)
	var anterior time.Time
	var ausente bool

	// Tres ciclos sem cliente no ar.
	for i := 0; i < 3; i++ {
		subiu, _, a := decideSubidaDoCliente(false, time.Time{}, anterior, ausente)
		if subiu {
			t.Fatalf("ciclo %d: nao ha cliente, nao devia decidir subida", i)
		}
		ausente = a
	}

	// O cliente aparece.
	subiu, apareceu, _ := decideSubidaDoCliente(true, agora, anterior, ausente)
	if !subiu || !apareceu {
		t.Fatalf("o cliente apareceu e a senha ficaria sem publicar: subiu=%v apareceu=%v", subiu, apareceu)
	}
}
