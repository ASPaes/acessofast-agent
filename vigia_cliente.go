// AcessoFast — vigia_cliente.go — AUTOCURA DO SERVICO DO CLIENTE.
//
// O agente e o cliente branded sao servicos independentes. O agente pode estar
// perfeitamente vivo — reportando presenca, aparecendo "Online" no painel — com o
// cliente parado, e nesse estado a maquina simplesmente NAO recebe conexao: o tecnico
// ve "O computador remoto esta offline" e a propria tela do endpoint diz "Servico nao
// esta em execucao".
//
// Diagnosticado em 25-26/08/2026 em dois clientes sem nenhuma relacao entre si (Cine
// Gracher e Delvale), o que descarta ambiente. A causa mais comum e o servico falhar
// no boot com erro 1053 (nao responde ao SCM em 30s numa maquina modesta disputando
// disco); tambem cobre queda e parada manual.
//
// Por que a cura tem que morar AQUI: quem opera o painel normalmente NAO tem
// administrador nos endpoints do cliente (o parque costuma ser de terceiros), entao
// "reiniciar o servico" nunca e uma recuperacao que o suporte faz sozinho — vira
// chamado, deslocamento, ou pedir reboot para alguem na loja. O agente, ao contrario,
// ja roda como SYSTEM na maquina e nao precisa de credencial de ninguem.
//
// O efeito e completo quando somado ao resto: subir o servico faz o clientStart mudar,
// detectClientRestart percebe e chama rotateAposRestartDoCliente() — entao a maquina
// volta a receber conexao E a senha volta a bater com a do painel, sem intervencao.

package main

import "time"

const (
	// clienteParadoTolerancia: quanto tempo o servico pode ficar parado antes de o
	// agente agir. Existe para nao brigar com uma parada legitima e curta — o corte do
	// free (checkHardCap) para e sobe o cliente de proposito, e uma atualizacao do
	// cliente tambem passa por aqui. Nenhuma dessas leva um minuto inteiro.
	clienteParadoTolerancia = 60 * time.Second

	// clienteSubirIntervalo: espaco entre tentativas. Servico que se recusa a subir
	// (binario corrompido, dependencia faltando) nao pode virar um laco de start a
	// cada tick. A cada 5 min o custo e desprezivel e a recuperacao continua garantida
	// assim que a causa sair do caminho.
	clienteSubirIntervalo = 5 * time.Minute
)

// checkClienteParado sobe o servico do cliente quando ele esta fora do ar. Chamada a
// cada tick do poll, na goroutine do worker.
//
// Leitura duvidosa do servico conta como PARADO de proposito? Nao — clientProcPID
// devolve false tanto para "parado" quanto para "nao consegui perguntar", e um start
// desnecessario e barato (o SCM ignora start de servico que ja roda), enquanto deixar
// de subir custa a maquina inteira inacessivel. Na duvida, sobe.
func (t *tailer) checkClienteParado() {
	if clienteVivo() {
		t.clienteParadoDesde = time.Time{}
		return
	}

	if t.clienteParadoDesde.IsZero() {
		t.clienteParadoDesde = time.Now()
		logln("CLIENTE: servico fora do ar — aguardando %s antes de subir", clienteParadoTolerancia)
		return
	}
	if time.Since(t.clienteParadoDesde) < clienteParadoTolerancia {
		return
	}
	if !t.ultimaSubidaCliente.IsZero() && time.Since(t.ultimaSubidaCliente) < clienteSubirIntervalo {
		return
	}

	t.ultimaSubidaCliente = time.Now()
	parado := time.Since(t.clienteParadoDesde).Truncate(time.Second)
	logln("CLIENTE: servico parado ha %s — subindo (autocura)", parado)
	// Em goroutine: mexer no SCM e esperar o servico responder leva segundos e nao
	// pode segurar o poll de deteccao de sessao. restartClientService e idempotente
	// para servico ja parado — o Stop falha e o Start faz o trabalho.
	go restartClientService()
}
