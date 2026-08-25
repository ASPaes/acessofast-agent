# AcessoFast — bootstrap do agente (rodada unica por maquina)
#
# POR QUE EXISTE
# O auto-update entrou no agente em 12/08/2026. Maquina com build anterior nao tem
# o cliente de update: nenhuma release que a gente publique alcanca ela, porque a
# peca que baixaria a release e justamente a que falta. Em 25/08/2026 isso valia
# para 105 das 123 maquinas online. Esta e a unica rodada manual necessaria —
# depois dela a maquina se atualiza sozinha, para sempre.
#
# USO (UMA linha; nunca colar multilinha em console de sessao remota):
#   powershell -ExecutionPolicy Bypass -File C:\Windows\Temp\bootstrap.ps1
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

function Falhar($msg) { Write-Host "FALHOU: $msg" -ForegroundColor Red; exit 1 }
function Ok($msg)     { Write-Host "OK: $msg"     -ForegroundColor Green }

# O PathName do servico pode vir com aspas e com argumentos. Um .Trim('"') simples
# devolveria o caminho junto com os argumentos e o Copy-Item erraria o alvo.
function CaminhoDoExe($pathName) {
  if ($pathName -match '^\s*"([^"]+)"') { return $Matches[1] }
  if ($pathName -match '^\s*(\S+\.exe)') { return $Matches[1] }
  return $pathName.Trim()
}

# --- 1) Pre-condicoes -------------------------------------------------------
$eAdmin = ([Security.Principal.WindowsPrincipal] `
  [Security.Principal.WindowsIdentity]::GetCurrent()
  ).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $eAdmin) { Falhar "abra o PowerShell como Administrador." }

$svc = Get-CimInstance Win32_Service -Filter "Name='$svcName'" -ErrorAction SilentlyContinue
if (-not $svc) {
  Falhar "o servico $svcName nao existe nesta maquina. Bootstrap e so para quem ja tem o agente; maquina nova precisa do instalador."
}

$exe = CaminhoDoExe $svc.PathName
if (-not (Test-Path $exe)) { Falhar "binario do servico nao encontrado em: $exe" }
Write-Host "Binario: $exe"

# --- 2) Ja estamos no alvo? -------------------------------------------------
$hashAtual = (Get-FileHash $exe -Algorithm SHA256).Hash
if ($hashAtual -eq $Sha256.ToUpper()) {
  if ((Get-Service $svcName).Status -ne 'Running') { Start-Service $svcName }
  Ok "ja esta em $Version; nada a fazer."
  exit 0
}

# --- 3) Baixar e CONFERIR antes de parar o servico --------------------------
# TLS 1.2 explicito: PowerShell 5.1 em Windows nao atualizado ainda negocia TLS 1.0
# por padrao e o GitHub recusa.
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$novo = Join-Path $env:TEMP "acessofast-agent-$Version.exe"
Write-Host "Baixando $Version ..."
try {
  Invoke-WebRequest -UseBasicParsing $url -OutFile $novo
} catch {
  Falhar "download: $($_.Exception.Message)"
}

$hashNovo = (Get-FileHash $novo -Algorithm SHA256).Hash
if ($hashNovo -ne $Sha256.ToUpper()) {
  Remove-Item $novo -Force -ErrorAction SilentlyContinue
  Falhar "SHA256 nao confere (esperado $Sha256, veio $hashNovo). Servico intacto."
}
Ok "hash conferido."

# --- 4) Trocar (a partir daqui o servico volta a subir em qualquer saida) ----
$backup = "$exe.bak-preupdate"
try {
  Copy-Item $exe $backup -Force
  Stop-Service $svcName -Force
  # O Windows pode segurar o arquivo por um instante depois do stop.
  $trocou = $false
  foreach ($tentativa in 1..10) {
    try { Copy-Item $novo $exe -Force; $trocou = $true; break }
    catch { Start-Sleep -Milliseconds 500 }
  }
  if (-not $trocou) { throw "nao consegui substituir o binario (arquivo em uso)." }
}
catch {
  $erro = $_.Exception.Message
  if (Test-Path $backup) { Copy-Item $backup $exe -Force -ErrorAction SilentlyContinue }
  Start-Service $svcName -ErrorAction SilentlyContinue
  Falhar "$erro (binario anterior restaurado, servico religado)."
}

Start-Service $svcName
Start-Sleep -Seconds 3
if ((Get-Service $svcName).Status -ne 'Running') {
  Copy-Item $backup $exe -Force
  Start-Service $svcName
  Falhar "o servico nao subiu com a versao nova; binario anterior restaurado."
}
Remove-Item $novo -Force -ErrorAction SilentlyContinue

# --- 5) O que o tecnico precisa levar para o painel -------------------------
$rid = ''
if (Test-Path "$baseDir\rustdesk_id") { $rid = (Get-Content "$baseDir\rustdesk_id" -Raw).Trim() }
$adotada = Test-Path "$baseDir\agent.token"

Ok "$Version instalada e rodando."
Write-Host ""
Write-Host "  rustdesk_id : $(if ($rid) { $rid } else { '(nao encontrado)' })"
Write-Host "  adotada     : $(if ($adotada) { 'sim' } else { 'NAO — aprovar no painel' })"
Write-Host ""
Write-Host "A versao aparece no painel no proximo 'presence' (ate 60s)."
