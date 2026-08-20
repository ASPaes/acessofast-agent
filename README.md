# acessofast-agent

Agente de sessão do AcessoFast. Roda como serviço na máquina do cliente, detecta
início e fim dos atendimentos remotos, gira a senha efêmera ao fim de cada sessão e
se atualiza sozinho.

**Um fonte, dois sistemas: Windows e macOS.**

## Onde cada coisa mora

A regra de negócio é comum aos dois sistemas. Só o que encosta no sistema
operacional tem duas versões.

| Arquivo | O que tem dentro |
|---|---|
| `main.go`, `enroll.go`, `rotate.go`, `sessao_socket.go`, `update.go` | A regra: detecção de sessão, matrícula, rotação da senha, auto-update. **Não sabe em que sistema roda.** |
| `plat_windows.go` | SCM, ACL por `icacls`, tabela TCP do `iphlpapi`, `schtasks`, caminhos do `ProgramData`. |
| `plat_darwin.go` | launchd, `chown`/`chmod`, `lsof`, `ps`, caminhos do `/Library/Application Support`. |
| `saida_macos.go` | Interpretação da saída dos comandos do macOS. **De propósito sem build tag** — ver abaixo. |

### A regra ao mexer

**Toda função de plataforma existe nos dois arquivos.** Se você adicionar uma em
`plat_windows.go` e esquecer o par em `plat_darwin.go`, o build do macOS quebra — e
quebra no pull request, não depois do merge.

**Bug encontrado em campo se conserta uma vez.** É por isso que a regra é comum. O
fantasma de sessão de 18/08/2026 (`#N` preso, "em atendimento" eterno) foi corrigido
num lugar só e vale para os dois sistemas. Se o macOS fosse uma cópia, o dia em que
alguém consertasse só de um lado, a outra plataforma voltaria a faturar errado em
silêncio.

**O que erra em silêncio fica fora da build tag.** `saida_macos.go` contém as funções
puras que interpretam a saída do `lsof` e do `ps`. Elas são exclusivas do macOS, mas
não levam `//go:build darwin`: sob a tag, só poderiam ser testadas numa máquina
Apple. Fora dela, `go test` as cobre em qualquer máquina, inclusive no CI do Windows.
Isso importa porque contar um socket a mais deixa a sessão aberta para sempre e
contar um a menos derruba a sessão de quem está trabalhando — nenhum dos dois dá
erro, os dois aparecem só na fatura do cliente.

**Caminho de credencial não muda por acidente.** `TestCaminhosWindowsNaoMudaram` e
`TestCaminhosDarwinNaoMudaram` travam onde ficam `agent.token`, `rustdesk_id`,
`rotate.pending`, `hold.until` e a pasta de update. Mudar um deles numa atualização
faz o agente subir sem achar a credencial e pedir matrícula de novo numa máquina já
adotada. Se a mudança for intencional, ela vem com migração junto — e com o teste
atualizado no mesmo commit.

## Verificando antes de abrir o PR

```sh
go vet ./...                                   # o sistema em que você está
GOOS=windows GOARCH=amd64 go vet ./...
GOOS=darwin  GOARCH=arm64 go vet ./...
GOOS=darwin  GOARCH=amd64 go vet ./...
go test ./...
```

`go test` roda os testes comuns mais os do **seu** sistema. Os do outro rodam no CI:
o workflow `verifica-plataformas.yml` compila os três alvos e executa os testes em
uma máquina Windows e uma macOS, em todo push de branch e todo pull request.

## Builds

| Workflow | O que faz |
|---|---|
| `build-agent.yml` | Windows. Publica o release assinado que alimenta o auto-update. |
| `build-agent-macos.yml` | macOS universal (Apple Silicon + Intel). **Ainda não publica release.** |
| `build-client.yml` | O cliente branded (RustDesk). Windows. |
| `build-installer.yml` | Monta o `AcessoFastSetup.exe`. |

Publicar um release **não** instala em ninguém: a versão precisa ser catalogada em
`agent_releases` e apontada como alvo, e dá para apontar em uma máquina só antes de
subir para a frota.

## Estado do macOS

O agente compila para macOS, mas **ainda não está pronto**. Falta:

1. Confirmar em máquina real os caminhos de config e log do cliente branded — ver o
   bloco `A CONFIRMAR EM MAQUINA REAL` no topo do `plat_darwin.go`. A detecção de
   sessão inteira depende de achar o log certo.
2. Certificado Developer ID, para assinar e notarizar.
3. O instalador `.pkg`, com o LaunchDaemon do agente.
