package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// servidorFalso responde como a session-ingest e guarda o que recebeu, sem sair para a rede.
type servidorFalso struct {
	sessao   string
	falha    bool
	recebido []map[string]string
}

func (s *servidorFalso) RoundTrip(r *http.Request) (*http.Response, error) {
	if s.falha {
		return nil, errors.New("sem rede")
	}
	var m map[string]string
	b, _ := io.ReadAll(r.Body)
	json.Unmarshal(b, &m)
	s.recebido = append(s.recebido, m)
	corpo := `{"ok":true,"session_id":"` + s.sessao + `"}`
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(corpo)), Header: http.Header{}}, nil
}

// inicios devolve os controladores dos 'start' recebidos ("" = start sem controlador).
func (s *servidorFalso) inicios() []string {
	var r []string
	for _, m := range s.recebido {
		if m["event"] == "start" {
			r = append(r, m["controller_rustdesk_id"])
		}
	}
	return r
}

func preparaServidorFalso(t *testing.T, sessao string) *servidorFalso {
	t.Helper()
	limpaHold(t)
	s := &servidorFalso{sessao: sessao}
	clienteAntes, tokenAntes, idAntes := httpClient, token, rustdeskID
	httpClient = &http.Client{Transport: s, Timeout: time.Second}
	token, rustdeskID = "token-de-teste", "111222333"
	marcaSessaoNoServidor("")
	t.Cleanup(func() {
		httpClient, token, rustdeskID = clienteAntes, tokenAntes, idAntes
		marcaSessaoNoServidor("")
	})
	return s
}

func confereInicios(t *testing.T, s *servidorFalso, quer ...string) {
	t.Helper()
	veio := s.inicios()
	if strings.Join(veio, ",") != strings.Join(quer, ",") || len(veio) != len(quer) {
		t.Fatalf("starts com controlador: quer %q, veio %q", quer, veio)
	}
}

// O caminho comum: o start sem controlador abre a sessao e o peer_id informa de onde veio.
func TestLoginInformaAOrigemUmaVezPorSessao(t *testing.T) {
	s := preparaServidorFalso(t, "sessao-1")
	tl := &tailer{open: make(map[string]time.Time)}

	tl.processLine("[info] #1 Connection opened from 10.0.0.1:5000.")
	tl.processLine("[info] #1 peer_id 115490044")
	confereInicios(t, s, "", "115490044")

	postEvent("heartbeat", "")
	tl.informaOrigem()
	postEvent("heartbeat", "")
	tl.informaOrigem()
	confereInicios(t, s, "", "115490044")
}

// O furo: a sessao mudou no servidor (novo clique em Conectar, ou ficha fechada e reaberta
// pelo heartbeat) com o agente no meio do mesmo acesso. Antes, ela ficava sem origem.
func TestSessaoNovaNoServidorRecebeAOrigem(t *testing.T) {
	s := preparaServidorFalso(t, "sessao-1")
	tl := &tailer{open: make(map[string]time.Time)}
	tl.processLine("[info] #1 Connection opened from 10.0.0.1:5000.")
	tl.processLine("[info] #1 peer_id 115490044")

	s.sessao = "sessao-2"
	postEvent("heartbeat", "")
	tl.informaOrigem()
	confereInicios(t, s, "", "115490044", "115490044")

	// Uma vez so: os heartbeats seguintes nao repetem.
	postEvent("heartbeat", "")
	tl.informaOrigem()
	confereInicios(t, s, "", "115490044", "115490044")
}

// Sem rede na hora de informar, tenta de novo no proximo heartbeat: e um log de seguranca.
func TestFalhaAoInformarTentaDeNovo(t *testing.T) {
	s := preparaServidorFalso(t, "sessao-1")
	tl := &tailer{open: make(map[string]time.Time)}
	tl.processLine("[info] #1 Connection opened from 10.0.0.1:5000.")
	tl.processLine("[info] #1 peer_id 115490044")

	s.sessao = "sessao-2"
	postEvent("heartbeat", "")
	s.falha = true
	tl.informaOrigem()
	s.falha = false
	confereInicios(t, s, "", "115490044")

	postEvent("heartbeat", "")
	tl.informaOrigem()
	confereInicios(t, s, "", "115490044", "115490044")
}

// Outro computador entrou no mesmo acesso: o servidor fica sabendo.
func TestOutroComputadorNaMesmaSessaoEInformado(t *testing.T) {
	s := preparaServidorFalso(t, "sessao-1")
	tl := &tailer{open: make(map[string]time.Time)}
	tl.processLine("[info] #1 Connection opened from 10.0.0.1:5000.")
	tl.processLine("[info] #1 peer_id 115490044")

	tl.processLine("[info] #2 Connection opened from 10.0.0.2:5000.")
	tl.processLine("[info] #2 peer_id 208146940")
	confereInicios(t, s, "", "115490044", "208146940")
	if tl.controllerID != "208146940" {
		t.Errorf("o controlador atual devia ser o ultimo a entrar, veio %q", tl.controllerID)
	}
}

// A mesma pessoa abrindo outra conexao (arquivos, por exemplo) nao gera aviso repetido.
func TestMesmoComputadorNaoRepeteAviso(t *testing.T) {
	s := preparaServidorFalso(t, "sessao-1")
	tl := &tailer{open: make(map[string]time.Time)}
	tl.processLine("[info] #1 Connection opened from 10.0.0.1:5000.")
	tl.processLine("[info] #1 peer_id 115490044")

	tl.processLine("[info] #2 Connection opened from 10.0.0.1:5001.")
	tl.processLine("[info] #2 peer_id 115490044")
	confereInicios(t, s, "", "115490044")
}

// Sem controlador conhecido (ainda nao passou do login) nao ha o que informar.
func TestSemControladorNaoInforma(t *testing.T) {
	s := preparaServidorFalso(t, "sessao-1")
	tl := &tailer{open: make(map[string]time.Time)}
	tl.processLine("[info] #1 Connection opened from 10.0.0.1:5000.")

	s.sessao = "sessao-2"
	postEvent("heartbeat", "")
	tl.informaOrigem()
	confereInicios(t, s, "")
}
