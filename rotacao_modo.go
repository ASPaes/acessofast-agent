// AcessoFast — rotacao_modo.go — MODO DE ROTACAO, decidido no servidor (Passo 1 do
// plano "Aposentar a Senha Rotativa").
//
// PARA QUE SERVE
// A senha efemera gira em quatro situacoes: fim de sessao autenticada, boot do agente,
// restart do servico do cliente e corte do hard_cap. As duas primeiras sao ROTINA; as
// duas ultimas sao RECUPERACAO e CORTE. O plano aposenta a rotina e mantem o resto — e
// este arquivo e onde cada gatilho de rotina pergunta "posso girar agora?".
//
// POR QUE O MODO VEM DO SERVIDOR
// Mesmo padrao do auto-update (agent_target_version): o modo e estado do banco em
// cascata device -> tenant -> global, resolvido na session-ingest e entregue no
// 'presence'. Isso da canario por maquina e rollback por UPDATE, sem rollout de binario.
// O agente guarda o ultimo valor em disco porque o BOOT acontece antes do primeiro
// presence — sem cache, todo boot cairia no padrao e giraria.
//
// OS MODOS
//
//	session       gira em tudo — o comportamento de antes deste arquivo, e o padrao
//	shadow        igual a session, mas LOGA o que install_only faria. Zero mudanca de
//	              comportamento: serve para validar a cascata e o cache em campo
//	install_only  nao gira em rotina (fim de sessao, boot). Gira no restart do cliente,
//	              que e recuperacao de divergencia, nao rotina
//	off           so o corte do hard_cap
//
// FORA DESTE ARQUIVO, e de proposito: o corte do hard_cap (checkHardCap) e a
// publicacao pos-adocao (publishSecretAfterAdoption) nao consultam o modo. O primeiro e
// o break-glass da cobranca; o segundo e o que faz o painel conhecer a senha de uma
// maquina nova. Nenhum dos dois e rotina.
//
// POR QUE O BOOT SAI DA ROTINA
// A linha de base achou falha de 35,5% quando a maquina nao era acessada ha mais de um
// dia, contra 13,5% logo depois de uma sessao — o contrario do que "senha velha no fim
// de sessao" preveria. Um candidato para esse gradiente e justamente o rotate-on-boot:
// cada reinicio e uma rotacao a mais, e cada rotacao e uma chance de o painel e a
// maquina divergirem (o caso BOMBONIERI03 de 25/08 foi um boot aplicando a senha num
// cliente parado). Tirar o boot e parte do experimento, nao efeito colateral.
//
// FALHA PARA O COMPORTAMENTO ANTIGO
// Modo desconhecido, arquivo ilegivel, servidor que nao mandou o campo: tudo cai em
// session. O pior caso de um defeito aqui e girar como antes — nunca parar de girar sem
// que alguem tenha pedido.
package main

import (
	"os"
	"path/filepath"
	"sync"
)

const (
	modoSession     = "session"
	modoShadow      = "shadow"
	modoInstallOnly = "install_only"
	modoOff         = "off"

	modoPadrao = modoSession
)

// rotacaoModoFile: ultimo modo recebido do servidor. Var, e nao const, para os testes
// apontarem para um diretorio temporario em vez de C:\ProgramData de quem roda.
//
// Nao e segredo, entao nao passa pelo hardenDir como o rotate.pending. Um admin local
// que escreva 'off' aqui ganha no maximo um ciclo de presence: o servidor reenvia o
// modo a cada presence e sobrescreve. E admin local ja esta fora do modelo de ameaca
// (furo #6 do FASE3-DESIGN) — le o agent.token, que vale muito mais.
var rotacaoModoFile = baseDir + `\rotacao.modo`

// gatilhoRotacao: as situacoes de ROTINA ou RECUPERACAO que consultam o modo.
type gatilhoRotacao int

const (
	gatilhoFimSessao gatilhoRotacao = iota
	gatilhoBoot
	gatilhoRestartCliente
)

func (g gatilhoRotacao) String() string {
	switch g {
	case gatilhoFimSessao:
		return "fim de sessao"
	case gatilhoBoot:
		return "boot"
	case gatilhoRestartCliente:
		return "restart do cliente"
	}
	return "gatilho desconhecido"
}

func modoValido(m string) bool {
	switch m {
	case modoSession, modoShadow, modoInstallOnly, modoOff:
		return true
	}
	return false
}

// decideRotacao e a tabela do plano, sem efeito colateral nenhum — e o que os testes
// cobrem gatilho a gatilho.
func decideRotacao(modo string, g gatilhoRotacao) bool {
	switch modo {
	case modoInstallOnly:
		// O restart do cliente e a rede de seguranca contra divergencia (ver
		// rotateAposRestartDoCliente): o cliente sobe lendo a senha do disco, que pode
		// nao ser a que o painel conhece. Isso nao e rotina, e segue girando.
		return g == gatilhoRestartCliente
	case modoOff:
		return false
	default:
		// session, shadow e qualquer valor desconhecido: gira.
		return true
	}
}

var (
	rotacaoMu   sync.Mutex
	rotacaoModo string // "" = ainda nao lido do disco neste processo
)

// modoRotacaoLocked devolve o modo em vigor. Chamar com rotacaoMu travado.
func modoRotacaoLocked() string {
	if rotacaoModo == "" {
		m := readTrim(rotacaoModoFile)
		if !modoValido(m) {
			m = modoPadrao
		}
		rotacaoModo = m
	}
	return rotacaoModo
}

// modoRotacao devolve o modo em vigor: o ultimo recebido do servidor, ou o padrao.
func modoRotacao() string {
	rotacaoMu.Lock()
	defer rotacaoMu.Unlock()
	return modoRotacaoLocked()
}

// gravaModoRotacao aplica o modo que veio na resposta da session-ingest. Valor
// invalido e ignorado — o modo em vigor fica — em vez de derrubado para o padrao: um
// campo corrompido no transporte nao pode ser o que faz uma maquina voltar a girar, nem
// o que faz ela parar.
func gravaModoRotacao(m string) {
	rotacaoMu.Lock()
	anterior := modoRotacaoLocked()
	if !modoValido(m) {
		rotacaoMu.Unlock()
		logln("ROTACAO modo ignorado: %q nao e um modo conhecido (segue %s)", m, anterior)
		return
	}
	if m == anterior {
		rotacaoMu.Unlock()
		return
	}
	rotacaoModo = m
	rotacaoMu.Unlock()

	// Disco depois da memoria: se a gravacao falhar, este processo ja obedece o modo
	// novo, e o proximo presence tenta gravar de novo. So um reinicio ANTES disso
	// voltaria ao modo antigo — e dura no maximo um ciclo.
	if err := os.MkdirAll(filepath.Dir(rotacaoModoFile), 0o700); err != nil {
		logln("ROTACAO WARN: nao criou %s: %v", filepath.Dir(rotacaoModoFile), err)
	} else if err := os.WriteFile(rotacaoModoFile, []byte(m), 0o600); err != nil {
		logln("ROTACAO WARN: nao gravou %s: %v", rotacaoModoFile, err)
	}
	logln("ROTACAO modo: %s -> %s (definido no painel)", anterior, m)
}

// podeRotacionar e o que cada gatilho de rotina chama antes de girar. Decide pelo modo
// em vigor e deixa no log o porque — e esse log que responde, no canario, se a maquina
// esta mesmo no modo que o painel mandou.
//
// Em session nao loga nada a mais: o log de 85 maquinas nao precisa de uma linha nova
// para dizer que tudo segue como antes.
func podeRotacionar(g gatilhoRotacao) bool {
	modo := modoRotacao()
	pode := decideRotacao(modo, g)
	switch {
	case modo == modoShadow:
		if decideRotacao(modoInstallOnly, g) {
			logln("ROTACAO shadow (%s): install_only tambem giraria aqui", g)
		} else {
			logln("ROTACAO shadow (%s): com install_only esta rotacao NAO aconteceria", g)
		}
	case !pode:
		logln("ROTATE suprimido (%s): modo %s — senha do painel mantida", g, modo)
	case modo != modoSession:
		logln("ROTACAO (%s) permitida no modo %s: e recuperacao, nao rotina", g, modo)
	}
	return pode
}
