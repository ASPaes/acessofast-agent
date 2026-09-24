package main

import "time"

// A subida do cliente branded vista de FORA do log: o processo esta no ar ou nao, e o
// horario em que comecou. Daqui sai uma decisao so, que serve a dois fins — encerrar a
// sessao fantasma e re-sincronizar a senha com o painel.
//
// Sem build tag de proposito: isto e regra, nao chamada de sistema. Roda nos testes de
// qualquer maquina, e e o CI do Windows que garante que a regra do macOS nao regrediu,
// e vice-versa. Mesma escolha de sessao_socket.go e saida_macos.go.

// decideSubidaDoCliente traduz o que o poll sabe sobre o processo do cliente em uma
// decisao. Sao quatro situacoes, e TRES delas chegam com o horario anterior zerado —
// foi por isso que uma passou despercebida:
//
//	processo fora do ar         -> nada; marca a ausencia, que e o que da sentido ao
//	                               caso APARECEU no ciclo seguinte
//	primeira leitura do agente  -> nada; quem rotaciona na subida do agente e o
//	                               rotateOnBoot, e girar duas vezes e desperdicio
//	estava ausente, agora no ar -> APARECEU: subiu lendo a config do disco
//	horario de inicio mudou     -> REINICIOU: as conexoes cairam sem 'closed'
//
// O caso APARECEU faltava ate 24/09/2026. No primeiro Mac o LaunchAgent do cliente nao
// carregou na instalacao e subiu horas depois; de zero para rodando nao e um restart, o
// agente so anotou o horario, e a senha ficou sem ser publicada no painel ate alguem
// reiniciar o agente a mao.
func decideSubidaDoCliente(noAr bool, inicio, anterior time.Time, ausenteAntes bool) (subiu, apareceu, ausenteAgora bool) {
	if !noAr {
		return false, false, true
	}
	reiniciou := !anterior.IsZero() && inicio.After(anterior)
	apareceu = anterior.IsZero() && ausenteAntes
	return reiniciou || apareceu, apareceu, false
}
