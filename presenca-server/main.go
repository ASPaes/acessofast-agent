// AcessoFast — servidor de presenca (roda na VPS do relay).
//
// PROBLEMA QUE ELE RESOLVE
// O agente carimbava presenca no Supabase a cada 180s: 480 invocacoes de edge
// function por maquina por dia, ~75% de todo o consumo do projeto. E entregava um
// status ruim de quebra — maquina desligada seguia "online" por ate 7 minutos,
// porque a queda so aparece quando os batimentos param de chegar.
//
// COMO FUNCIONA
// O agente abre um POST que fica PENDURADO ate ~55s. Enquanto a conexao esta
// aberta, a maquina esta viva. Quando ela cai, sabemos na hora. So as MUDANCAS de
// estado vao para o Supabase, em lote — e mudanca e rara por natureza.
//
// POR QUE LONG-POLL E NAO WEBSOCKET
// O agente tem UMA dependencia (golang.org/x/sys) e nao fala WebSocket. Long-poll
// e HTTP comum: `net/http` da stdlib nos dois lados, zero dependencia nova, passa
// em firewall corporativo que barra WebSocket, e a deteccao de queda e a mesma —
// o que prova vida e o socket aberto, nao o protocolo em cima dele.
//
// CUSTO NO SUPABASE
// Zero invocacao de edge function. As mudancas vao por PostgREST (rest/v1), que
// nao conta na cota de funcoes. Em regime, um dia inteiro da frota cabe em algumas
// centenas de chamadas.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"sync"
	"syscall"
	"time"
)

const (
	// Quanto tempo a requisicao fica pendurada. 55s fica abaixo do timeout de 60s
	// que proxies e balanceadores costumam aplicar — passar disso faz a conexao
	// ser cortada pelo meio do caminho e vira reconexao desnecessaria.
	janelaPoll = 55 * time.Second

	// Sem reabrir dentro disto, a maquina e dada como offline. Precisa ser MAIOR
	// que janelaPoll mais o tempo de reabrir: no desligamento limpo o socket cai e
	// percebemos na hora; no tombo seco (tomada, travamento) o socket fica
	// pendurado e este prazo e o que fecha a conta. 90s cobre uma reabertura
	// perdida sem dar falso offline em rede ruim.
	prazoSilencio = 90 * time.Second

	// De quanto em quanto tempo varremos o mapa procurando quem silenciou.
	intervaloVarredura = 10 * time.Second

	// Lotes de mudanca sao acumulados e enviados juntos. Com a frota inteira
	// reconectando depois de uma queda de rede, uma chamada por maquina viraria
	// uma tempestade no Supabase — e e justamente nesse momento que ele precisa
	// estar respondendo.
	intervaloDescarga = 5 * time.Second

	// A lista de tokens validos e sincronizada, nao consultada por conexao.
	// Perguntar ao banco a cada conexao devolveria o custo que viemos eliminar.
	intervaloSyncTokens = 5 * time.Minute
)

var reRustdeskID = regexp.MustCompile(`^[0-9]{6,12}$`)

// ---------------------------------------------------------------------------
// Estado em memoria
// ---------------------------------------------------------------------------

type maquina struct {
	online      bool
	ultimoSinal time.Time
}

type servidor struct {
	mu       sync.RWMutex
	maquinas map[string]*maquina

	// tokens[rustdesk_id] = sha256 hex do agent_token
	tokensMu sync.RWMutex
	tokens   map[string]string

	// mudancas pendentes de envio ao Supabase
	pendenteMu sync.Mutex
	pendente   map[string]mudanca

	supabaseURL string
	serviceKey  string
	http        *http.Client
}

type mudanca struct {
	RustdeskID string `json:"rustdesk_id"`
	Online     bool   `json:"online"`
	Em         string `json:"em"`
}

func novoServidor(url, key string) *servidor {
	return &servidor{
		maquinas:    make(map[string]*maquina),
		tokens:      make(map[string]string),
		pendente:    make(map[string]mudanca),
		supabaseURL: url,
		serviceKey:  key,
		// Timeout generoso: o Supabase e o unico destino e uma descarga lenta e
		// melhor que uma descarga perdida.
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// marcaViva registra atividade e devolve true se o estado MUDOU (offline->online).
func (s *servidor) marcaViva(id string) bool {
	agora := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	m, existe := s.maquinas[id]
	if !existe {
		s.maquinas[id] = &maquina{online: true, ultimoSinal: agora}
		return true
	}
	m.ultimoSinal = agora
	if !m.online {
		m.online = true
		return true
	}
	return false
}

// varre procura quem passou do prazo de silencio e devolve os ids que cairam.
func (s *servidor) varre() []string {
	limite := time.Now().Add(-prazoSilencio)
	var cairam []string

	s.mu.Lock()
	defer s.mu.Unlock()
	for id, m := range s.maquinas {
		if m.online && m.ultimoSinal.Before(limite) {
			m.online = false
			cairam = append(cairam, id)
		}
	}
	return cairam
}

// enfileira guarda a mudanca para a proxima descarga. Chaveado por id: se a
// maquina oscilar duas vezes dentro da janela, vale o ultimo estado — mandar o
// pisca-pisca inteiro nao ajuda ninguem e polui o historico do painel.
func (s *servidor) enfileira(id string, online bool) {
	s.pendenteMu.Lock()
	defer s.pendenteMu.Unlock()
	s.pendente[id] = mudanca{
		RustdeskID: id,
		Online:     online,
		Em:         time.Now().UTC().Format(time.RFC3339),
	}
}

// ---------------------------------------------------------------------------
// Conversa com o Supabase (PostgREST — NAO edge function)
// ---------------------------------------------------------------------------

func (s *servidor) chamaRPC(ctx context.Context, nome string, corpo any) ([]byte, error) {
	payload, err := json.Marshal(corpo)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/rest/v1/rpc/%s", s.supabaseURL, nome)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("apikey", s.serviceKey)
	req.Header.Set("authorization", "Bearer "+s.serviceKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rpc %s devolveu %d: %s", nome, resp.StatusCode, buf.String())
	}
	return buf.Bytes(), nil
}

// descarrega envia o lote acumulado. Em erro, as mudancas VOLTAM para a fila:
// perder uma transicao deixa o painel mentindo ate a proxima, e o Supabase estar
// fora do ar por um minuto nao pode custar o estado da frota.
func (s *servidor) descarrega(ctx context.Context) {
	s.pendenteMu.Lock()
	if len(s.pendente) == 0 {
		s.pendenteMu.Unlock()
		return
	}
	lote := make([]mudanca, 0, len(s.pendente))
	for _, m := range s.pendente {
		lote = append(lote, m)
	}
	s.pendente = make(map[string]mudanca)
	s.pendenteMu.Unlock()

	if _, err := s.chamaRPC(ctx, "aplicar_presenca", map[string]any{"p_lote": lote}); err != nil {
		log.Printf("descarga falhou (%d mudancas voltam pra fila): %v", len(lote), err)
		s.pendenteMu.Lock()
		for _, m := range lote {
			// Nao sobrescreve mudanca mais nova que chegou enquanto enviavamos.
			if _, jaTem := s.pendente[m.RustdeskID]; !jaTem {
				s.pendente[m.RustdeskID] = m
			}
		}
		s.pendenteMu.Unlock()
		return
	}
	log.Printf("descarga ok: %d mudancas", len(lote))
}

func (s *servidor) sincronizaTokens(ctx context.Context) error {
	bruto, err := s.chamaRPC(ctx, "tokens_de_presenca", map[string]any{})
	if err != nil {
		return err
	}
	var linhas []struct {
		RustdeskID string `json:"rustdesk_id"`
		Hash       string `json:"agent_token_hash"`
	}
	if err := json.Unmarshal(bruto, &linhas); err != nil {
		return err
	}
	novo := make(map[string]string, len(linhas))
	for _, l := range linhas {
		if l.RustdeskID != "" && l.Hash != "" {
			novo[l.RustdeskID] = l.Hash
		}
	}
	// Lista vazia e tratada como erro: e quase certo que veio de falha e nao de
	// uma frota que sumiu. Trocar o mapa por um vazio derrubaria todo mundo.
	if len(novo) == 0 {
		return errors.New("lista de tokens veio vazia — mantendo a anterior")
	}
	s.tokensMu.Lock()
	s.tokens = novo
	s.tokensMu.Unlock()
	log.Printf("tokens sincronizados: %d maquinas", len(novo))
	return nil
}

func (s *servidor) tokenConfere(id, token string) bool {
	s.tokensMu.RLock()
	hash, existe := s.tokens[id]
	s.tokensMu.RUnlock()
	if !existe {
		return false
	}
	return hash == sha256Hex(token)
}

// ---------------------------------------------------------------------------
// O endpoint
// ---------------------------------------------------------------------------

type pedido struct {
	RustdeskID string `json:"rustdesk_id"`
	AgentToken string `json:"agent_token"`
}

type resposta struct {
	OK      bool `json:"ok"`
	Proximo int  `json:"proximo_em_segundos"`
}

func (s *servidor) handlePresenca(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var p pedido
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&p); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if !reRustdeskID.MatchString(p.RustdeskID) || p.AgentToken == "" {
		http.Error(w, "campos invalidos", http.StatusBadRequest)
		return
	}
	if !s.tokenConfere(p.RustdeskID, p.AgentToken) {
		// 401 aqui NAO gera laco: o agente trata como "volto no proximo ciclo",
		// com a espera normal. Vale para maquina recem-adotada que ainda nao
		// entrou na sincronizacao — ela entra em ate 5 minutos.
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if mudou := s.marcaViva(p.RustdeskID); mudou {
		s.enfileira(p.RustdeskID, true)
	}

	// Aqui esta o coracao: a resposta fica PENDURADA. O que mantem a maquina
	// "viva" nao e o corpo que devolvemos, e o socket permanecer aberto.
	ctx := r.Context()
	timer := time.NewTimer(janelaPoll)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		// A maquina foi embora (desligou, rede caiu, servico parou). Marcar
		// offline JA seria errado: um proxy no meio do caminho tambem derruba
		// conexao, e o agente reabre em seguida. Quem decide e o prazo de
		// silencio na varredura — uma fonte so de verdade, sem corrida.
		return
	case <-timer.C:
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(resposta{OK: true, Proximo: 0})
	}
}

func (s *servidor) handleSaude(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	total, online := len(s.maquinas), 0
	for _, m := range s.maquinas {
		if m.online {
			online++
		}
	}
	s.mu.RUnlock()

	s.tokensMu.RLock()
	tokens := len(s.tokens)
	s.tokensMu.RUnlock()

	s.pendenteMu.Lock()
	fila := len(s.pendente)
	s.pendenteMu.Unlock()

	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true, "maquinas_conhecidas": total, "online": online,
		"tokens_carregados": tokens, "fila_de_mudancas": fila,
	})
}

func main() {
	url := os.Getenv("SUPABASE_URL")
	key := os.Getenv("SUPABASE_SERVICE_ROLE_KEY")
	porta := os.Getenv("PORTA")
	if porta == "" {
		porta = "8787"
	}
	if url == "" || key == "" {
		log.Fatal("faltam SUPABASE_URL e SUPABASE_SERVICE_ROLE_KEY")
	}

	s := novoServidor(url, key)

	ctx, cancela := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancela()

	// Sem a lista de tokens nao da para autenticar ninguem: se a primeira carga
	// falhar, o servico nao sobe. Melhor nao subir do que subir recusando a frota.
	if err := s.sincronizaTokens(ctx); err != nil {
		log.Fatalf("primeira carga de tokens falhou: %v", err)
	}

	go func() {
		t := time.NewTicker(intervaloSyncTokens)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := s.sincronizaTokens(ctx); err != nil {
					log.Printf("sync de tokens falhou (mantendo a lista atual): %v", err)
				}
			}
		}
	}()

	go func() {
		t := time.NewTicker(intervaloVarredura)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				for _, id := range s.varre() {
					s.enfileira(id, false)
				}
			}
		}
	}()

	go func() {
		t := time.NewTicker(intervaloDescarga)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.descarrega(ctx)
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/presenca", s.handlePresenca)
	mux.HandleFunc("/saude", s.handleSaude)

	srv := &http.Server{
		Addr:    ":" + porta,
		Handler: mux,
		// ReadTimeout curto (o corpo e minusculo), WriteTimeout MAIOR que a janela
		// de poll — senao o proprio servidor corta a conexao que ele esta segurando
		// de proposito, e a frota inteira entra em reconexao a cada ciclo.
		ReadTimeout:  10 * time.Second,
		WriteTimeout: janelaPoll + 15*time.Second,
		IdleTimeout:  2 * time.Minute,
	}

	go func() {
		<-ctx.Done()
		desligar, cancelaDesligar := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelaDesligar()
		_ = srv.Shutdown(desligar)
	}()

	log.Printf("servidor de presenca ouvindo em :%s", porta)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("servidor caiu: %v", err)
	}

	// Ultima descarga na saida: as mudancas na fila ainda valem, e um deploy
	// nao deveria custar o estado da frota.
	final, cancelaFinal := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelaFinal()
	s.descarrega(final)
	log.Print("servidor de presenca encerrado")
}
