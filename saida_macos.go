// AcessoFast — agente: interpretacao da saida dos comandos do macOS.
//
// POR QUE ESTE ARQUIVO NAO TEM `//go:build darwin`, sendo que so o macOS usa isto:
//
// Aqui mora a parte do lado Mac que pode errar EM SILENCIO. Contar um socket a mais
// e a sessao nunca termina (fantasma, "em atendimento" pra sempre); contar um a menos
// e a sessao de quem esta trabalhando e cortada no meio. Nenhum dos dois da erro,
// nenhum aparece em log — os dois so aparecem na fatura do cliente.
//
// Se estivesse sob build tag, este codigo so poderia ser testado numa maquina Apple,
// que e exatamente a maquina que nao temos a mao. Sem tag, ele compila e e testado
// em qualquer lugar — inclusive no CI do Windows e na maquina de quem programa.
//
// Sao funcoes PURAS: recebem texto, devolvem valor. Nao chamam comando nenhum, nao
// tocam disco nem rede. Quem executa lsof e ps e o plat_darwin.go; aqui so
// interpretamos o que eles disseram. No binario Windows isso custa algumas centenas
// de bytes e nao roda nunca.
package main

import (
	"strconv"
	"strings"
	"time"
)

// contaSocketsSessao interpreta a saida de
//
//	lsof -nP -a -p <pid> -iTCP -sTCP:ESTABLISHED -Fn
//
// e devolve quantos sockets sao de SESSAO — isto e, todos menos o vinculo que a
// maquina ociosa mantem com o rendezvous (portas portaNatTest e portaRendezvous).
//
// O formato -F e "um campo por linha, prefixado pela letra do campo". So as linhas
// que comecam com 'n' carregam endereco; as outras ('p' de pid, e o que mais vier em
// versoes futuras) sao ignoradas de proposito, e nao por descuido: ignorar o
// desconhecido e o que faz este parser sobreviver a uma atualizacao do macOS.
//
// Uma linha de socket conectado tem a forma
//
//	n192.168.0.5:52341->203.0.113.9:21117
//
// e em IPv6 os enderecos vem entre colchetes:
//
//	n[2804:14d::1]:52341->[2001:db8::1]:21117
//
// Por isso a porta remota sai do ULTIMO ":" depois da seta, e nao do primeiro: num
// endereco v6 os dois-pontos aparecem varias vezes, e pegar o primeiro leria lixo.
func contaSocketsSessao(saidaLsof string) int {
	total := 0
	for _, linha := range strings.Split(saidaLsof, "\n") {
		linha = strings.TrimSpace(linha)
		if !strings.HasPrefix(linha, "n") {
			continue
		}
		seta := strings.Index(linha, "->")
		if seta < 0 {
			continue // sem par remoto: e socket em escuta, nao sessao
		}
		remoto := linha[seta+2:]
		i := strings.LastIndex(remoto, ":")
		if i < 0 {
			continue
		}
		porta, err := strconv.Atoi(strings.TrimSpace(remoto[i+1:]))
		if err != nil || porta <= 0 {
			continue
		}
		switch porta {
		case portaNatTest, portaRendezvous:
			continue // maquina ociosa falando com o servidor
		}
		total++
	}
	return total
}

// parseLstart interpreta a saida de `ps -o lstart= -p <pid>`, que sai no formato
//
//	Wed Aug 20 08:14:33 2026
//
// sem fuso nenhum — o horario e o LOCAL da maquina, por isso a localizacao entra por
// parametro (o chamador passa time.Local; o teste passa uma fixa, senao o resultado
// mudaria conforme o fuso de quem roda o teste).
//
// (zero, false) quando nao da pra ler. O chamador trata isso como "nao sei", e
// quem nao sabe nao age: um horario errado aqui faria o agente concluir que o
// cliente reiniciou e encerrar uma sessao que esta viva.
func parseLstart(saida string, loc *time.Location) (time.Time, bool) {
	s := strings.Join(strings.Fields(saida), " ") // normaliza o espaco duplo do dia < 10
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", s, loc)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// pidDoServidor acha, na saida de `ps -axo pid=,args=`, o processo do cliente que
// RECEBE as sessoes — e so ele.
//
// PROVA DE CAMPO (01/09/2026, sessao viva): o cliente se divide em dois processos,
//
//	38229 /Applications/AcessoFast.app/Contents/MacOS/AcessoFast
//	38975 /Applications/AcessoFast.app/Contents/MacOS/AcessoFast --cm
//
// e o socket da sessao estava SO no primeiro; o --cm (gerenciador de conexoes, a
// janelinha que mostra quem esta conectado) nao tinha socket nenhum. Vigiar o
// processo errado daria "sem socket" com sessao viva — e o agente encerraria a
// sessao de quem esta trabalhando, achando que era fantasma.
//
// O ARGUMENTO DEPENDE DE COMO O CLIENTE FOI INICIADO, e isso me pegou:
//
//	instalado como servico   AcessoFast --server     <- launchd, o caso do cliente
//	aberto a mao pela pessoa AcessoFast              <- sem argumento
//
// A primeira versao desta funcao exigia AUSENCIA de argumento, porque na coleta de
// campo o app tinha sido aberto a mao. Com o .pkg instalado, o servidor passou a
// rodar com --server e o agente parou de enxergar o cliente: dava "servico fora do
// ar" e a autocura reiniciava um cliente que estava perfeitamente vivo, de 5 em 5
// minutos, pra sempre.
//
// A regra correta e por EXCLUSAO: o que nunca serve e o --cm (gerenciador de
// conexoes, sem socket). Entre os demais, o --server tem preferencia — quando ele
// existe, e ele quem segura a sessao.
func pidDoServidor(saidaPs, exe string) (uint32, bool) {
	var comServer, semArgumento uint32
	for _, linha := range strings.Split(saidaPs, "\n") {
		campos := strings.Fields(linha)
		if len(campos) < 2 || campos[1] != exe {
			continue
		}
		pid64, err := strconv.ParseUint(campos[0], 10, 32)
		if err != nil || pid64 == 0 {
			continue
		}
		pid := uint32(pid64)
		argumentos := campos[2:]
		if temArgumento(argumentos, "--cm") {
			continue
		}
		if temArgumento(argumentos, "--server") {
			comServer = pid
			continue
		}
		if len(argumentos) == 0 {
			semArgumento = pid
		}
	}
	if comServer != 0 {
		return comServer, true
	}
	if semArgumento != 0 {
		return semArgumento, true
	}
	return 0, false
}

func temArgumento(argumentos []string, alvo string) bool {
	for _, a := range argumentos {
		if a == alvo {
			return true
		}
	}
	return false
}
