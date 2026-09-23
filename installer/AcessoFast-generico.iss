; ============================================================================
;  AcessoFast — Instalador GENERICO (v3.0 — fluxo B, matricula por adocao)
;  ---------------------------------------------------------------------------
;  UM binario, pra QUALQUER cliente. O cliente NAO digita nada.
;
;    Cliente baixa em acessofast.com.br/download -> clica 2x -> UAC -> pronto.
;
;  Diferenca pro instalador do fluxo A (que pedia/embutia o "Codigo da Empresa"):
;  aqui NAO existe codigo e NAO existe --enroll. O agente sobe SEM token, entra
;  em MODO MATRICULA (matricula.go): gera nonce+token na propria maquina, faz
;  check-in no claim-register e fica esperando ser ADOTADO. Do outro lado, o
;  tecnico adota a maquina PELO ID (que o cliente le na tela do AcessoFast) no
;  painel -> o token e liberado -> o agente vira modo sessao. O ID nao e segredo;
;  a prova de posse e o nonce, que nunca sai da maquina.
;
;  Serve tambem pra deploy em massa (GPO/Intune) com /VERYSILENT: cada maquina
;  se registra e o admin adota por ID. Nenhum segredo por-maquina.
;
;  ---------------------------------------------------------------------------
;  Herdado do instalador provado (NAO mexer — cada item foi verificado em maquina real):
;
;  1. PAYLOAD: AcessoFastClient.exe (artefato do build-client.yml) +
;     acessofast-agent.exe (artefato do build-agent.yml — precisa SER O NOVO,
;     com o modo matricula; nao reusar binario velho).
;
;  2. SEM injecao de TOML. O cliente branded ja carrega relay+chave hardcoded e
;     o custom_.txt (app-name + 3 chaves de first-config). O instalador nativo
;     dele copia isso. So CONFERIMOS que o custom_.txt sobreviveu.
;
;  3. NOME DO SERVICO DO CLIENTE: 'AcessoFast' (get_app_name via custom_.txt),
;     NAO 'Rustdesk'. Confirmado por Get-Service.
;
;  4. DIRETORIO DO AGENTE: {autopf}\AcessoFast Agent — separado do
;     C:\Program Files\AcessoFast do cliente (senao o --uninstall do cliente
;     levaria o agente junto).
;
;  5. AGENTE COMO SERVICO SCM (SYSTEM), NAO Scheduled Task (Task em sessao 0
;     morre no loader 0xC0000142). Provado.
;
;  BUILD: GitHub Actions. ASSINATURA obrigatoria em producao (senao SmartScreen).
; ============================================================================

#define MyAppName        "AcessoFast Agent"
; 3.1.0 (2026-08-17): payload passa a levar o agente COM auto-update
; (release 2026.08.15-f602a40, sha256 6d95b4af...e0e0cb). A partir deste
; instalador, maquina nova nunca mais precisa de sessao remota so por versao
; de agente: ela se declara no painel e obedece ao alvo de update.
#define MyAppVersion     "3.1.1"
#define MyAppPublisher   "AcessoFast"
#define MyAppURL         "https://acessofast.com.br"
#define AgentServiceName "AcessoFastAgent"

; Payload do cliente branded (artefato do workflow build-client.yml).
#ifndef ClientExe
  #define ClientExe "AcessoFastClient.exe"
#endif

[Setup]
AppId={{8F3A1C42-7B9E-4D51-A6C8-2E5F9B0D3A71}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
; NAO usar {autopf}\AcessoFast: e onde o cliente branded se instala.
DefaultDirName={autopf}\AcessoFast Agent
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
DisableDirPage=yes
OutputBaseFilename=AcessoFastSetup
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
; Cria servico via SCM -> exige admin sempre.
PrivilegesRequired=admin
ArchitecturesInstallIn64BitMode=x64compatible
ArchitecturesAllowed=x64compatible
UninstallDisplayName={#MyAppName}

; ---------------- Marca (logo/icone AcessoFast) -----------------------------
SetupIconFile=branding\app.ico
WizardImageFile=branding\wizard-large.png
WizardSmallImageFile=branding\wizard-small.png
UninstallDisplayIcon={app}\AcessoFast.ico

[Languages]
Name: "brazilianportuguese"; MessagesFile: "compiler:Languages\BrazilianPortuguese.isl"

; A tela final instrui o cliente a informar o ID ao suporte (o passo que libera o acesso).
[Messages]
brazilianportuguese.FinishedHeadingLabel=Quase la — falta liberar o acesso
brazilianportuguese.FinishedLabel=O AcessoFast foi instalado e vai abrir agora mostrando o ID deste computador.%n%nInforme esse ID ao seu suporte de TI. Assim que ele liberar, o acesso remoto passa a funcionar. Voce nao precisa digitar mais nada aqui.

[Files]
; Binario unico do agente: roda como servico E (neste fluxo) se matricula sozinho
; ao subir sem token. Fica permanente em {app}.
Source: "payload\acessofast-agent.exe"; DestDir: "{app}"; Flags: ignoreversion
; Icone da marca (so pra aparecer em Adicionar/Remover Programas).
Source: "branding\app.ico"; DestDir: "{app}"; DestName: "AcessoFast.ico"; Flags: ignoreversion
; Cliente branded: descartavel, extraido em {tmp}. O instalador NATIVO dele decide o destino.
Source: "payload\{#ClientExe}"; Flags: dontcopy

[Run]
; No fim, abre o cliente pro usuario VER O ID (que ele passa pro suporte).
; skipifsilent: em deploy em massa nao abre UI. nowait: nao trava o instalador.
Filename: "C:\Program Files\AcessoFast\AcessoFast.exe"; Description: "Abrir o AcessoFast e ver o ID desta maquina"; Flags: postinstall nowait skipifsilent

[UninstallRun]
Filename: "{sys}\sc.exe"; Parameters: "stop {#AgentServiceName}";   Flags: runhidden; RunOnceId: "StopAgent"
Filename: "{sys}\sc.exe"; Parameters: "delete {#AgentServiceName}"; Flags: runhidden; RunOnceId: "DelAgent"

[UninstallDelete]
; token, nonce e estado de matricula sao credenciais — nao deixa para tras.
Type: filesandordirs; Name: "C:\ProgramData\AcessoFast"

[Code]
const
  { Servico do cliente branded. NAO e 'Rustdesk' -- vem do app-name do custom_.txt
    via get_app_name(). Confirmado por Get-Service em maquina real. }
  CLIENTE_SVC = 'AcessoFast';

  { Onde o instalador NATIVO do cliente se instala. Confirmado na tela dele e no
    InstallLocation do registro de Uninstall. }
  CLIENTE_EXE = 'C:\Program Files\AcessoFast\AcessoFast.exe';

  { Prova de que o branding sobreviveu. Sem este arquivo o app-name volta a
    "RustDesk", o cliente le %APPDATA%\RustDesk\ e perde as 3 chaves de first-config. }
  CLIENTE_CUSTOM = 'C:\Program Files\AcessoFast\custom_.txt';

  SVC_TIMEOUT      = 60;   { segundos aguardando o servico do cliente subir }
  POS_INSTALL_WAIT = 25;   { o --silent-install NAO retorna; aguarda antes de olhar }

{ --------------------------------------------------------------------------
  Passo 1 — Cliente branded silencioso, como Servico Windows
  -------------------------------------------------------------------------- }
{ Exit codes do sc.exe: 0=ok, 1056=ALREADY_RUNNING, 1060=DOES_NOT_EXIST }
function ServicoExiste(const Nome: String): Boolean;
var RC: Integer;
begin
  Result := Exec(ExpandConstant('{sys}\sc.exe'), 'query ' + Nome, '',
                 SW_HIDE, ewWaitUntilTerminated, RC) and (RC = 0);
end;

function GarantirServicoRodando(const Nome: String; TimeoutSec: Integer): Boolean;
var RC, Elapsed: Integer;
begin
  Result := False;
  Elapsed := 0;
  while Elapsed < TimeoutSec do
  begin
    if Exec(ExpandConstant('{sys}\sc.exe'), 'start ' + Nome, '',
            SW_HIDE, ewWaitUntilTerminated, RC) then
    begin
      if (RC = 0) or (RC = 1056) then
      begin
        Result := True;
        Exit;
      end;
    end;
    Sleep(3000);
    Elapsed := Elapsed + 3;
  end;
end;

{ O --silent-install NAO retorna: dispara, aguarda por tempo, depois confere o servico. }
function InstalarCliente(): Boolean;
var ResultCode: Integer; Setup: String;
begin
  Result := False;
  ExtractTemporaryFile('{#ClientExe}');
  Setup := ExpandConstant('{tmp}\{#ClientExe}');

  if not Exec(Setup, '--silent-install', '', SW_HIDE, ewNoWait, ResultCode) then
  begin
    Log('Falha ao disparar o instalador do cliente AcessoFast.');
    Exit;
  end;
  Sleep(POS_INSTALL_WAIT * 1000);

  { Fallback: se o silent-install nao registrou o servico, forca. }
  if not ServicoExiste(CLIENTE_SVC) then
  begin
    if not FileExists(CLIENTE_EXE) then
    begin
      Log('AcessoFast.exe nao encontrado em ' + CLIENTE_EXE + ' -- instalacao nao concluiu.');
      Exit;
    end;
    Log('Servico ausente apos silent-install; forcando --install-service.');
    Exec(CLIENTE_EXE, '--install-service', '', SW_HIDE, ewNoWait, ResultCode);
    Sleep(15000);
  end;

  if not ServicoExiste(CLIENTE_SVC) then
  begin
    Log('Servico ' + CLIENTE_SVC + ' nao foi criado.');
    Exit;
  end;

  Result := GarantirServicoRodando(CLIENTE_SVC, SVC_TIMEOUT);
  if not Result then
    Log('Servico ' + CLIENTE_SVC + ' nao subiu em ' + IntToStr(SVC_TIMEOUT) + 's.');
end;

{ --------------------------------------------------------------------------
  Passo 2 — Verificacao da identidade (custom_.txt sobreviveu)
  Falhar aqui e melhor que instalar um cliente sem identidade -- esse defeito
  nao da erro, so nao funciona (perde as chaves, disputa %APPDATA%\RustDesk\).
  -------------------------------------------------------------------------- }
function VerificarIdentidade(): Boolean;
begin
  Result := FileExists(CLIENTE_CUSTOM);
  if not Result then
    Log('custom_.txt AUSENTE em ' + CLIENTE_CUSTOM +
        ' -- o cliente perdeu o app-name e as chaves de first-config.');
end;

{ --------------------------------------------------------------------------
  Passo 3 — Agente como Servico Windows via SCM

  Aqui o servico sobe SEM token -> o agente entra em MODO MATRICULA sozinho
  (gera nonce+token, claim-register, aguarda adocao). NENHUMA matricula acontece
  no instalador; o proprio agente cuida disso ao rodar.
  NAO usar Scheduled Task: Task como SYSTEM em sessao 0 morre no loader. Provado.
  -------------------------------------------------------------------------- }
function InstalarAgente(): Boolean;
var RC: Integer; BinPath: String;
begin
  BinPath := ExpandConstant('{app}\acessofast-agent.exe');

  { Idempotencia: reinstalacao por cima nao pode falhar em "ja existe". }
  Exec(ExpandConstant('{sys}\sc.exe'), 'stop {#AgentServiceName}', '',
       SW_HIDE, ewWaitUntilTerminated, RC);
  Exec(ExpandConstant('{sys}\sc.exe'), 'delete {#AgentServiceName}', '',
       SW_HIDE, ewWaitUntilTerminated, RC);
  Sleep(1500);

  { sc.exe exige o espaco depois do "=" — sintaxe legada, nao e engano. }
  Result := Exec(ExpandConstant('{sys}\sc.exe'),
    'create {#AgentServiceName} binPath= "' + BinPath + '" ' +
    'start= auto obj= LocalSystem DisplayName= "AcessoFast Agent"',
    '', SW_HIDE, ewWaitUntilTerminated, RC);

  if (not Result) or (RC <> 0) then
  begin
    Log('sc create falhou. RC=' + IntToStr(RC));
    Result := False;
    Exit;
  end;

  Exec(ExpandConstant('{sys}\sc.exe'),
    'description {#AgentServiceName} "Registra as sessoes de suporte remoto do AcessoFast."',
    '', SW_HIDE, ewWaitUntilTerminated, RC);

  { Reinicia sozinho se cair — o agente e a fonte do faturamento. }
  Exec(ExpandConstant('{sys}\sc.exe'),
    'failure {#AgentServiceName} reset= 86400 actions= restart/5000/restart/10000/restart/30000',
    '', SW_HIDE, ewWaitUntilTerminated, RC);

  Exec(ExpandConstant('{sys}\sc.exe'), 'start {#AgentServiceName}', '',
       SW_HIDE, ewWaitUntilTerminated, RC);
  Log('Agente instalado e iniciado (entrando em modo matricula).');
end;

{ --------------------------------------------------------------------------
  Orquestracao — SEM passo de matricula (o agente se matricula sozinho)
  -------------------------------------------------------------------------- }
procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep <> ssPostInstall then Exit;

  { ORDEM: cliente -> verifica identidade -> agente. O agente sobe por ultimo,
    sem token, e comeca a matricula. NAO ha --enroll aqui. }

  WizardForm.StatusLabel.Caption := 'Instalando o AcessoFast...';
  if not InstalarCliente() then
  begin
    MsgBox('Falha ao instalar o mecanismo de acesso remoto.', mbCriticalError, MB_OK);
    Abort();
  end;

  WizardForm.StatusLabel.Caption := 'Verificando a configuracao...';
  if not VerificarIdentidade() then
  begin
    MsgBox('A instalacao do AcessoFast nao esta completa (configuracao ausente).' + #13#10 +
           'Execute o instalador novamente como Administrador.', mbCriticalError, MB_OK);
    Abort();
  end;

  WizardForm.StatusLabel.Caption := 'Ativando o agente...';
  if not InstalarAgente() then
  begin
    MsgBox('Falha ao instalar o servico do agente.', mbCriticalError, MB_OK);
    Abort();
  end;
end;
