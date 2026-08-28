# AcessoFast — diagnostico de "maquina aparece offline / nao conecta"
#
# QUANDO USAR
# O painel mostra a maquina ONLINE (o agente esta reportando presence) mas a sessao
# nunca conecta, ou o RustDesk diz que o peer esta offline. Isso e um sintoma de
# DUAS coisas diferentes que so se distinguem aqui dentro:
#
#   1. ID DIVERGENTE. O agente le C:\ProgramData\AcessoFast\rustdesk_id ANTES de
#      olhar o .toml do cliente (discoverRustdeskID em main.go) e nunca reconfere.
#      Se o cliente trocou de ID depois da matricula — o que o servidor de ID forca
#      quando detecta colisao, tipico de maquinas clonadas da mesma imagem — o
#      agente segue reportando o ID VELHO. A presenca continua chegando (o velho e
#      o que esta no address_book) e o painel tenta conectar num ID que nao existe
#      mais. Sintoma: agente online, sessao nunca confirma.
#
#   2. CLIENTE ERRADO. Instalacao que deixou o RustDesk generico em vez do cliente
#      da marca. O agente fica cego (so le o log em ...\Roaming\AcessoFast\log\
#      server\) e o painel mira num cliente que nao esta ali.
#
# USO — UMA linha, em Prompt de Comando OU PowerShell, COMO ADMINISTRADOR:
#
#   powershell -NoProfile -ExecutionPolicy Bypass -Command "if(-not([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(544)){Write-Host 'ABRA COMO ADMINISTRADOR.' -ForegroundColor Red;exit 1};[Net.ServicePointManager]::SecurityProtocol=3072;iwr -UseBasicParsing https://raw.githubusercontent.com/ASPaes/acessofast-agent/main/diagnostico.ps1 -OutFile C:\Windows\Temp\diag.ps1;& C:\Windows\Temp\diag.ps1"
#
# So LE. Nao para servico, nao troca arquivo, nao escreve nada.

$ErrorActionPreference = 'Continue'
$baseDir = 'C:\ProgramData\AcessoFast'

# Os caminhos que importam ficam sob C:\Windows\ServiceProfiles\LocalService e
# exigem elevacao. Sem admin o Test-Path/Get-Content lanca AcessoNegado e a leitura
# ingenua confunde "nao existe" com "nao pude ler" — o veredito passa a AFIRMAR que
# nenhuma sessao chegou quando na verdade nao olhou. Diagnostico que mente e pior
# que nenhum, entao toda leitura registra a falha aqui e o veredito se recusa a
# concluir se esta lista nao estiver vazia.
$naoPudeLer = New-Object System.Collections.Generic.List[string]

function LeuOuAnotou([scriptblock]$acao, [string]$oQue) {
  try { & $acao }
  catch {
    # SO acesso negado conta como "nao pude ler". Arquivo que legitimamente nao
    # existe (ItemNotFoundException) e RESULTADO, nao falha — varios caminhos aqui
    # sao alternativas e a maioria nao existe em maquina nenhuma. Tratar os dois
    # igual bloquearia o veredito em toda maquina saudavel.
    $tipo = $_.Exception.GetType().Name
    if ($tipo -match 'UnauthorizedAccess|Security') { $naoPudeLer.Add("$oQue -> $tipo") }
    $null
  }
}

function Secao($t) { Write-Host ""; Write-Host "== $t ==" -ForegroundColor Cyan }

Secao "SERVICOS"
# O cliente da marca registra o servico como 'AcessoFast' (vem do app-name do
# custom_.txt). Um servico chamado 'RustDesk' aqui significa cliente generico —
# instalacao incompleta, nao a nossa.
$svcs = @(Get-CimInstance Win32_Service -EA SilentlyContinue |
          Where-Object Name -match 'acesso|fast|rustdesk')
if ($svcs.Count) {
  $svcs | ForEach-Object { Write-Host ("  {0,-20} {1,-9} {2}" -f $_.Name, $_.State, $_.PathName) }
} else { Write-Host "  nenhum" }

Secao "ID QUE O AGENTE REPORTA (cache)"
$idCache = ''
if (Test-Path -LiteralPath "$baseDir\rustdesk_id") {
  $idCache = (Get-Content -LiteralPath "$baseDir\rustdesk_id" -Raw).Trim()
  Write-Host "  $baseDir\rustdesk_id = $idCache"
} else {
  Write-Host "  $baseDir\rustdesk_id : NAO EXISTE"
}

Secao "ID REAL DO CLIENTE (config em disco)"
# Mesmos caminhos do discoverRustdeskID, mais os do cliente generico pra flagrar
# o caso 2. O do LocalService e o que vale: e sob ele que o servico roda.
$configs = @(
  'C:\Windows\ServiceProfiles\LocalService\AppData\Roaming\AcessoFast\config\AcessoFast.toml'
  'C:\Windows\ServiceProfiles\LocalService\AppData\Roaming\AcessoFast\config\AcessoFast2.toml'
  'C:\Users\*\AppData\Roaming\AcessoFast\config\AcessoFast.toml'
  'C:\Users\*\AppData\Roaming\AcessoFast\config\AcessoFast2.toml'
  'C:\Windows\ServiceProfiles\LocalService\AppData\Roaming\RustDesk\config\RustDesk.toml'
  'C:\Users\*\AppData\Roaming\RustDesk\config\RustDesk.toml'
)
$idsVistos = @()
$achou = $false
foreach ($g in $configs) {
  # O Where-Object nao e enfeite: quando a leitura falha a funcao devolve $null, e
  # @($null) e um array de UM elemento nulo — sem o filtro o laco roda uma vez com
  # $f vazio e imprime uma linha fantasma.
  $achados = @(LeuOuAnotou { Get-ChildItem -Path $g -ErrorAction Stop } "config $g") |
             Where-Object { $_ }
  foreach ($f in $achados) {
    $achou = $true
    $txt = LeuOuAnotou { Get-Content -LiteralPath $f.FullName -Raw -ErrorAction Stop } "conteudo $($f.FullName)"
    $m = [regex]::Match([string]$txt, "(?m)^\s*id\s*=\s*['`"]?([0-9]{6,})")
    $id = if ($m.Success) { $m.Groups[1].Value } else { '(sem id)' }
    if ($m.Success) { $idsVistos += $id }
    Write-Host "  id=$id  $($f.FullName)"
    Write-Host "      modificado em $($f.LastWriteTime)"
  }
}
if (-not $achou) { Write-Host "  nenhum arquivo de config encontrado" }

Secao "LOG DE SESSAO (o que o agente vigia)"
# Sem este arquivo o agente nunca detecta inicio/fim de sessao. Ele nasce na
# PRIMEIRA sessao recebida — ausente significa que nenhuma sessao chegou nesta
# maquina, o que ja e a resposta.
$logs = @(
  'C:\Windows\ServiceProfiles\LocalService\AppData\Roaming\AcessoFast\log\server\AcessoFast_rCURRENT.log'
  'C:\Windows\System32\config\systemprofile\AppData\Roaming\AcessoFast\log\server\AcessoFast_rCURRENT.log'
)
$temLog = $false
foreach ($l in $logs) {
  $i = LeuOuAnotou { Get-Item -LiteralPath $l -ErrorAction Stop } "log $l"
  if ($i) {
    $temLog = $true
    Write-Host "  existe: $l"
    Write-Host "      $($i.Length) bytes, ultima escrita $($i.LastWriteTime)"
  }
}
if (-not $temLog -and $naoPudeLer.Count -eq 0) {
  Write-Host "  NAO existe — nenhuma sessao jamais chegou nesta maquina"
} elseif (-not $temLog) {
  Write-Host "  nao encontrado (mas houve leitura barrada; ver o fim)"
}

Secao "VEREDITO"
# Antes de qualquer conclusao: se alguma leitura foi barrada, os dados estao
# incompletos e QUALQUER veredito abaixo seria chute com cara de fato.
if ($naoPudeLer.Count) {
  Write-Host "  NAO DA PARA CONCLUIR — estas leituras foram barradas:" -ForegroundColor Red
  $naoPudeLer | ForEach-Object { Write-Host "    $_" }
  Write-Host ""
  Write-Host "  Quase sempre e falta de elevacao. Rode de novo COMO ADMINISTRADOR."
  Write-Host ""
  exit 1
}

$clienteMarca = @($svcs | Where-Object Name -eq 'AcessoFast').Count -gt 0
$clienteGen   = @($svcs | Where-Object Name -eq 'RustDesk').Count -gt 0
$idsUnicos    = @($idsVistos | Sort-Object -Unique)

if (-not $clienteMarca -and $clienteGen) {
  Write-Host "  CLIENTE ERRADO: esta maquina tem RustDesk generico, nao o cliente da marca." -ForegroundColor Yellow
  Write-Host "  O painel nao alcanca esse cliente. Conserto: rodar o AcessoFastSetup.exe atual."
}
elseif (-not $clienteMarca) {
  Write-Host "  SEM CLIENTE: nao ha servico de cliente remoto rodando." -ForegroundColor Yellow
  Write-Host "  Conserto: rodar o AcessoFastSetup.exe atual."
}
elseif ($idCache -and $idsUnicos.Count -and ($idsUnicos -notcontains $idCache)) {
  Write-Host "  ID DIVERGENTE: o agente reporta $idCache mas o cliente esta em $($idsUnicos -join ', ')." -ForegroundColor Yellow
  Write-Host "  O painel tenta conectar no ID errado — dai o 'offline'."
  Write-Host "  Conserto: apagar $baseDir\rustdesk_id e reiniciar o servico AcessoFastAgent;"
  Write-Host "  o agente redescobre o ID e o painel passa a receber o certo no proximo presence."
  Write-Host "  ATENCAO: o device muda de rustdesk_id no painel — confirmar com quem cuida do cadastro."
}
elseif (-not $temLog) {
  Write-Host "  CLIENTE OK E ID BATE, mas nenhuma sessao jamais chegou." -ForegroundColor Yellow
  Write-Host "  O cliente provavelmente nao esta registrado no servidor de ID (rede/firewall),"
  Write-Host "  ou a senha de acesso nao esta configurada. Conferir na tela do cliente na maquina."
}
else {
  Write-Host "  Nada obviamente errado aqui. Mandar esta saida inteira." -ForegroundColor Green
}
Write-Host ""
