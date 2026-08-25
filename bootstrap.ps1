# AcessoFast — bootstrap do agente (rodada unica por maquina)
#
# POR QUE EXISTE
# O auto-update entrou no agente em 12/08/2026. Maquina com build anterior nao tem
# o cliente de update: nenhuma release que a gente publique alcanca ela, porque a
# peca que baixaria a release e justamente a que falta. Em 25/08/2026 isso valia
# para 105 das 123 maquinas online. Esta e a unica rodada manual necessaria —
# depois dela a maquina se atualiza sozinha, para sempre.
#
# USO — UMA linha, colada em Prompt de Comando OU PowerShell, COMO ADMINISTRADOR.
# Nunca colar bloco multilinha: em console de sessao remota as linhas chegam como
# eventos separados e sem garantia de ordem (ja rodaram invertidas em campo, 25/08).
#
#   powershell -NoProfile -ExecutionPolicy Bypass -Command "if(-not([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(544)){Write-Host 'ABRA COMO ADMINISTRADOR.' -ForegroundColor Red;exit 1};[Net.ServicePointManager]::SecurityProtocol=3072;iwr -UseBasicParsing https://raw.githubusercontent.com/ASPaes/acessofast-agent/main/bootstrap.ps1 -OutFile C:\Windows\Temp\bs.ps1;& C:\Windows\Temp\bs.ps1"
#
# Por que a linha e assim e nao um `iwr` solto:
#   - o tecnico costuma abrir o Prompt de Comando, nao o PowerShell, e la `iwr` nao
#     existe. Prefixar com `powershell -Command` faz a mesma linha servir nos dois.
#   - o `iwr` que baixa ESTE arquivo tambem precisa de TLS 1.2 (Windows 8.1 negocia
#     1.0 por padrao). Nao adianta o script setar; sem isso ele nem chega a ser baixado.
#   - sem elevacao o erro seria um "acesso negado" ao gravar em C:\Windows\Temp, antes
#     de qualquer checagem daqui. A verificacao no lancador troca isso por um recado.
#
# SEGURO PARA REPETIR: se a maquina ja estiver na versao alvo, nao faz nada.
#
# GARANTIA: o servico nunca fica parado. Todo caminho de erro depois do Stop-Service
# restaura o binario anterior e sobe o servico de novo. Baixar e conferir o hash
# acontecem ANTES de parar qualquer coisa — falha de rede nao tira a maquina do ar.
#
# O QUE NAO E TOCADO: C:\ProgramData\AcessoFast (agent.token, rustdesk_id). Nao ha
# rematricula, o dispositivo continua o mesmo no painel.

[CmdletBinding()]
param(
  # Alvo do bootstrap. NAO precisa ser a versao mais nova — basta ser >= 12/08/2026,
  # porque a partir dai o proprio agente busca o alvo global sozinho.
  [string]$Version = '2026.08.18-7ada713',
  [string]$Sha256  = '9bc5a3d2408144e4886e43fb09880226fcd12baa0756ada8e864ca53e01816b9'
)

$ErrorActionPreference = 'Stop'
$svcName = 'AcessoFastAgent'
$baseDir = 'C:\ProgramData\AcessoFast'
$url     = "https://github.com/ASPaes/acessofast-agent/releases/download/$Version/acessofast-agent.exe"

# NAO usar $env:TEMP: em maquina cujo usuario do Windows tem acento no nome, ele vem
# como nome curto 8.3 (C:\Users\USUARIO~2\...) e o provider de caminho do PowerShell
# se perde nele — o Remove-Item resolveu so ate "C:\Users\USUARIO~2" e estourou.
# C:\Windows\Temp e ASCII, existe em toda maquina e e gravavel por SYSTEM/admin.
$tmpDir = 'C:\Windows\Temp'

function Falhar($msg) { Write-Host "FALHOU: $msg" -ForegroundColor Red; exit 1 }
function Ok($msg)     { Write-Host "OK: $msg"     -ForegroundColor Green }

# Apagar o temporario e cosmetico e NUNCA pode derrubar o script: com
# $ErrorActionPreference='Stop' um erro aqui matava a execucao antes do resumo, e o
# tecnico ficava sem o rustdesk_id e sem saber se a maquina precisa ser adotada —
# que e a unica parte acionavel da saida. Aconteceu em campo (25/08).
function Limpar($caminho) {
  try { Remove-Item -LiteralPath $caminho -Force -ErrorAction SilentlyContinue } catch { }
}

# O PathName do servico pode vir com aspas e com argumentos. Um .Trim('"') simples
# devolveria o caminho junto com os argumentos e o Copy-Item erraria o alvo.
function CaminhoDoExe($pathName) {
  if ($pathName -match '^\s*"([^"]+)"') { return $Matches[1] }
  if ($pathName -match '^\s*(\S+\.exe)') { return $Matches[1] }
  return $pathName.Trim()
}

# --- 1) Pre-condicoes -------------------------------------------------------
# Piso da frota e o Windows 8.1 (6.3.9600, 7 maquinas em 25/08) — PowerShell 4.0,
# que ja traz Get-FileHash, Get-CimInstance e Invoke-WebRequest. Nao ha Windows 7 na
# frota; se aparecer um, ele vem com PS 2.0 e morreria no Get-FileHash com um erro
# obscuro. Melhor dizer o motivo.
if ($PSVersionTable.PSVersion.Major -lt 4) {
  Falhar "precisa de PowerShell 4.0+ (Get-FileHash); esta maquina tem $($PSVersionTable.PSVersion)."
}

$eAdmin = ([Security.Principal.WindowsPrincipal] `
  [Security.Principal.WindowsIdentity]::GetCurrent()
  ).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $eAdmin) { Falhar "abra o PowerShell como Administrador." }

$svc = Get-CimInstance Win32_Service -Filter "Name='$svcName'" -ErrorAction SilentlyContinue
if (-not $svc) {
  Falhar "o servico $svcName nao existe nesta maquina. Bootstrap e so para quem ja tem o agente; maquina nova precisa do instalador."
}

$exe = CaminhoDoExe $svc.PathName
if (-not (Test-Path -LiteralPath $exe)) { Falhar "binario do servico nao encontrado em: $exe" }
Write-Host "Binario: $exe"

# --- 2) Ja estamos no alvo? -------------------------------------------------
# -LiteralPath em TODO cmdlet de caminho: sem ele o PowerShell interpreta o caminho
# como padrao com curinga, e basta um colchete no perfil do usuario pra quebrar.
$hashAtual = (Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash
if ($hashAtual -eq $Sha256.ToUpper()) {
  if ((Get-Service $svcName).Status -ne 'Running') { Start-Service $svcName }
  Ok "ja esta em $Version; nada a fazer."
  exit 0
}

# --- 3) Baixar e CONFERIR antes de parar o servico --------------------------
# TLS 1.2 explicito: no Windows 8.1 (PowerShell 4.0 / .NET 4.5) o padrao ainda e
# Ssl3+Tls1.0 e o GitHub recusa a conexao. 3072 e o valor de Tls12 escrito como
# numero de proposito — em .NET antigo o NOME Tls12 nao existe no enum e a linha
# estouraria antes de chegar ao download. O -bor preserva o que ja estava ligado.
try {
  [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor 3072
} catch {
  Falhar "esta maquina nao consegue habilitar TLS 1.2; o GitHub nao aceita conexao sem ele."
}
$novo = Join-Path $tmpDir "acessofast-agent-$Version.exe"
Write-Host "Baixando $Version ..."
try {
  Invoke-WebRequest -UseBasicParsing $url -OutFile $novo
} catch {
  Falhar "download: $($_.Exception.Message)"
}

$hashNovo = (Get-FileHash -LiteralPath $novo -Algorithm SHA256).Hash
if ($hashNovo -ne $Sha256.ToUpper()) {
  Limpar $novo
  Falhar "SHA256 nao confere (esperado $Sha256, veio $hashNovo). Servico intacto."
}
Ok "hash conferido."

# --- 4) Trocar (a partir daqui o servico volta a subir em qualquer saida) ----
$backup = "$exe.bak-preupdate"
try {
  Copy-Item -LiteralPath $exe -Destination $backup -Force
  Stop-Service $svcName -Force
  # O Windows pode segurar o arquivo por um instante depois do stop.
  $trocou = $false
  foreach ($tentativa in 1..10) {
    try { Copy-Item -LiteralPath $novo -Destination $exe -Force; $trocou = $true; break }
    catch { Start-Sleep -Milliseconds 500 }
  }
  if (-not $trocou) { throw "nao consegui substituir o binario (arquivo em uso)." }
}
catch {
  $erro = $_.Exception.Message
  if (Test-Path -LiteralPath $backup) {
    Copy-Item -LiteralPath $backup -Destination $exe -Force -ErrorAction SilentlyContinue
  }
  Start-Service $svcName -ErrorAction SilentlyContinue
  Falhar "$erro (binario anterior restaurado, servico religado)."
}

Start-Service $svcName
Start-Sleep -Seconds 3
if ((Get-Service $svcName).Status -ne 'Running') {
  Copy-Item -LiteralPath $backup -Destination $exe -Force
  Start-Service $svcName
  Falhar "o servico nao subiu com a versao nova; binario anterior restaurado."
}

# --- 5) O que o tecnico precisa levar para o painel -------------------------
# ANTES da limpeza, de proposito: a troca ja deu certo e o resumo e o que o tecnico
# leva pro painel. Nada meramente cosmetico pode vir na frente dele.
$rid = ''
if (Test-Path -LiteralPath "$baseDir\rustdesk_id") {
  $rid = (Get-Content -LiteralPath "$baseDir\rustdesk_id" -Raw).Trim()
}
$adotada = Test-Path -LiteralPath "$baseDir\agent.token"

Ok "$Version instalada e rodando."
Write-Host ""
Write-Host "  rustdesk_id : $(if ($rid) { $rid } else { '(nao encontrado)' })"
Write-Host "  adotada     : $(if ($adotada) { 'sim' } else { 'NAO — aprovar no painel' })"
Write-Host ""
Write-Host "A versao aparece no painel no proximo 'presence' (ate 60s)."

Limpar $novo
