// AcessoFast — reboot_em_acesso.go — O ACESSO VOLTA DEPOIS DE REINICIAR.
//
// O tecnico reinicia o computador que esta acessando (pelo botao do app ou pelo Iniciar
// do Windows) e o app dele fica tentando voltar com a MESMA senha da sessao. Sem isto o
// rotateOnBoot girava a senha assim que o Windows ligava, e a volta dava "Senha
// incorreta": no teste de 21/10/2026 no PC do Vitor, a senha girou as 09:16:38 e o app
// chegou na maquina as 09:16:48.
//
// COMO
// Enquanto ha acesso AUTENTICADO aberto, o agente mantem o holdFile (o "hold de
// manutencao" do §4.3, que o rotateOnBoot ja sabia ler mas ninguem escrevia) com prazo
// de holdAcessoValidade a frente, renovado a cada heartbeat. Se a maquina reinicia no
// meio do acesso, o arquivo sobrevive e o boot nao gira. O fim normal do acesso gira a
// senha como sempre, e toda rotacao aplicada apaga o hold (rotateNow).
//
// SE NINGUEM VOLTAR
// rotacaoAdiadaAposBoot gira depois de esperaVoltaAposBoot, se naquele momento nao houver
// acesso aberto. A senha vista antes do reboot morre alguns minutos depois, em vez de
// sobreviver ate o proximo atendimento (o furo R2a/#8 que o rotate-on-boot fechou).
//
// So acesso AUTENTICADO segura a senha: tentativa recusada nao viu senha valida nenhuma,
// e nao pode ser o que impede a rotacao do boot.

package main

import (
	"os"
	"sync/atomic"
	"time"
)

const (
	// holdAcessoValidade: quanto tempo depois do ultimo sinal de acesso aberto um boot
	// ainda conta como "reiniciou no meio do acesso". Folga para o Windows instalar
	// atualizacao no reinicio, que passa facil de 10 minutos.
	holdAcessoValidade = 30 * time.Minute

	// esperaVoltaAposBoot: quanto o agente espera o tecnico voltar antes de girar a senha
	// que ficou para tras. O app do tecnico tenta voltar por 5 minutos contados da queda;
	// 10 minutos contados do boot cobrem isso com sobra.
	esperaVoltaAposBoot = 10 * time.Minute
)

// acessoAberto espelha len(t.open) > 0 para quem nao roda na goroutine do worker (a
// rotacao adiada). Atualizado a cada tick do poll.
var acessoAberto atomic.Bool

// renovaHold empurra o prazo do hold enquanto ha acesso autenticado aberto. Chamado ao
// autenticar (cobre o reinicio logo no comeco do acesso, antes do primeiro heartbeat) e
// a cada heartbeat.
func (t *tailer) renovaHold() {
	if len(t.open) == 0 || !t.autenticada {
		return
	}
	escreveHold(time.Now().Add(holdAcessoValidade))
}

// escreveHold grava o prazo do hold. Falha de disco so custa o comportamento antigo (a
// senha gira no boot), entao loga e segue.
func escreveHold(ate time.Time) {
	novo := readTrim(holdFile) == ""
	if err := os.WriteFile(holdFile, []byte(ate.Format(time.RFC3339)), 0o644); err != nil {
		logln("HOLD WARN: nao gravou %s: %v", holdFile, err)
		return
	}
	if novo {
		logln("HOLD: acesso autenticado aberto — se a maquina reiniciar agora, a senha fica para o tecnico voltar")
	}
}

// apagaHold tira o hold. Chamado quando uma senha nova e aplicada: a senha que o hold
// protegia ja morreu, entao nao ha mais o que segurar.
func apagaHold() {
	if readTrim(holdFile) == "" {
		return
	}
	if err := os.Remove(holdFile); err != nil && !os.IsNotExist(err) {
		logln("HOLD WARN: nao apagou %s: %v", holdFile, err)
		return
	}
	logln("HOLD: removido (senha nova aplicada)")
}

// rotacaoAdiadaAposBoot e o que o rotateOnBoot faz quando encontra o hold: segura a
// senha para o tecnico voltar e so gira mais tarde se ninguem voltou. Roda na goroutine
// do rotateOnBoot.
func rotacaoAdiadaAposBoot() {
	logln("REBOOT em acesso: senha mantida para o tecnico voltar; gira em %s se ninguem voltar", esperaVoltaAposBoot)
	time.Sleep(esperaVoltaAposBoot)

	if acessoAberto.Load() {
		logln("REBOOT em acesso: ha acesso aberto — a senha gira no fim dele, como sempre")
		return
	}
	if readTrim(holdFile) == "" {
		// Uma rotacao no meio do caminho (o tecnico voltou e ja saiu) apagou o hold.
		return
	}
	// O que sobrou e a rotacao do boot, adiada: mesma regra dela.
	if !podeRotacionar(gatilhoBoot) {
		apagaHold()
		return
	}
	if !esperaCliente(esperaClienteLimite) {
		logln("ROTATE boot adiado: cliente fora do ar — rotacao adiada (o restart do cliente re-sincroniza)")
		return
	}
	logln("ROTATE boot adiado: ninguem voltou em %s — girando a senha", esperaVoltaAposBoot)
	rotateNow()
}
