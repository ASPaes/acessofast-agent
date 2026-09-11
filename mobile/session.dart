// AcessoFast — telemetria de sessão (etapa 6) + rotação da senha (etapa 7).
//
// Copiado pelo CI para flutter/lib/acessofast/session.dart.
// Porte de main.go (tailer + postEvent) e rotate.go (Fase 2).
//
// ---------------------------------------------------------------------------
// COMO DETECTA SESSÃO — e por que por OBSERVAÇÃO, não por hook
// ---------------------------------------------------------------------------
// O agente Windows lê o log do cliente e pareia "#N Connection opened/closed".
// Aqui a lista de clientes conectados está na memória do mesmo processo
// (gFFI.serverModel.clients), então observamos ela num timer.
//
// Escolhemos OBSERVAR em vez de patchar addConnection/onClientRemove por sed:
// o build parte de uma TAG do rustdesk/rustdesk, e patch dentro de método do
// upstream quebra a cada upgrade. Observar depende só de `clients`, que é API
// pública do ServerModel. Mesma filosofia do agente Windows, que também deriva
// estado de observação em vez de instrumentar o RustDesk.
//
// Transições (idêntico ao main.go). "Ativo" = conexão ACEITA (_emAtendimento):
//   0 -> N ativos : 'start'   (+ controller_rustdesk_id, dispara auto-adoção)
//   N ativos      : 'heartbeat' a cada 20s
//   N -> 0        : 'end'     + ROTAÇÃO da senha
//   ocioso        : 'presence' a cada 60s — 10 min se o servidor diz que o
//                   aparelho não está no cadastro (_presenceIntervalSemCadastro)
//
// ---------------------------------------------------------------------------
// INVARIANTE DA ROTAÇÃO (copiada do rotate.go — não inverter a ordem)
// ---------------------------------------------------------------------------
// O painel NUNCA pode conhecer uma senha que ainda não está no aparelho.
//   1. aplica a senha nova no cliente
//   2. só então reporta ao painel
// Se (1) falhar, a senha ANTIGA continua nos dois lados: consistente, sem
// lockout. Se (2) falhar, persistimos a pendência e um laço reenvia — nesse
// intervalo o painel serve a senha velha e o técnico pode falhar uma vez, que é
// auto-recuperável e preferível a travar o acesso.

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:flutter_hbb/common.dart';
import 'package:flutter_hbb/models/platform_model.dart';
// kUsePermanentPassword vive aqui, nao no common.dart.
import 'package:flutter_hbb/models/server_model.dart';
import 'package:http/http.dart' as http;
import 'package:path_provider/path_provider.dart';

const String _fnBase = 'https://plmfyibyrowbgjjyblcl.supabase.co/functions/v1';
const String _ingestUrl = '$_fnBase/session-ingest';
const String _rotateUrl = '$_fnBase/rotate-device-secret';

const String _anonKey =
    'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6InBsbWZ5aWJ5cm93YmdqanlibGNsIiwicm9sZSI6ImFub24iLCJpYXQiOjE3ODM2NDMyNjIsImV4cCI6MjA5OTIxOTI2Mn0.grcQYqN3fHvFTWI0AFPWG66k1wONuGqZ5yMt07qcjxE';

// Versao deste build, reportada em todo POST a session-ingest (agent_version) e
// exibida na coluna "Agente" do painel. Mesmo contrato do `-X main.version` do
// agente Windows: formato AAAA.MM.DD-<sha7>, data primeiro e em largura fixa pra
// o painel ordenar builds comparando string. Injetado pelo CI com
//
//   flutter build apk --dart-define=ACESSOFAST_VERSION=2026.08.07-a1b2c3d
//
// Build local fica "dev", que o painel trata como versao desconhecida.
const String _agentVersion =
    String.fromEnvironment('ACESSOFAST_VERSION', defaultValue: 'dev');

// Mesmos intervalos do main.go.
const Duration _pollInterval = Duration(seconds: 3);
const Duration _heartbeatInterval = Duration(seconds: 20);
const Duration _presenceInterval = Duration(seconds: 60);
// Aparelho que o servidor diz não estar no cadastro (404) ou cujo token ele não
// reconhece (401). O caso comum é a matrícula esquecida que o servidor aquietou
// (ver agent.dart): o app tem credencial, mas ninguém adotou. Ninguém vê o
// status de um aparelho fora do cadastro, então 60s não serviria a ninguém e
// custaria 1.440 chamadas por dia por celular. A regra de NÃO espaçar o
// presence vale para aparelho cadastrado, cujo status o técnico enxerga — esse
// segue a 60s.
const Duration _presenceIntervalSemCadastro = Duration(minutes: 10);
const Duration _retryInterval = Duration(seconds: 30);
const Duration _httpTimeout = Duration(seconds: 20);

// Política de senha — ESPELHA provision-device-secret (sem ambíguos 0 O 1 l I).
// Se divergir, o painel e o aparelho geram senhas de classes diferentes e a
// validação do servidor recusa.
const String _pwLower = 'abcdefghijkmnpqrstuvwxyz';
const String _pwUpper = 'ABCDEFGHJKLMNPQRSTUVWXYZ';
const String _pwDigits = '23456789';
const String _pwAlphabet = _pwLower + _pwUpper + _pwDigits;
const int _pwLen = 20;

const String _credentialsFile = 'acessofast_agent.json';
const String _pendingFile = 'acessofast_rotate.pending';
// Marca (uma vez por aparelho) de que a senha ja foi sincronizada com o painel
// logo apos a matricula. Ver _maybeInitialSync.
const String _pwSyncFlagFile = 'acessofast_pwsync.done';

void _log(String msg) {
  // ignore: avoid_print
  print('[acessofast/session] $msg');
}

Map<String, String> get _headers => {
      'Content-Type': 'application/json',
      'apikey': _anonKey,
      'Authorization': 'Bearer $_anonKey',
    };

Future<File> _file(String name) async {
  final dir = await getApplicationSupportDirectory();
  return File('${dir.path}/$name');
}

// ---------------------------------------------------------------------------
// Credencial (gravada pelo agent.dart ao ser adotado)
// ---------------------------------------------------------------------------

String? _token;
String? _rustdeskId;

Future<bool> _loadCredentials() async {
  if (_token != null && _rustdeskId != null) return true;
  try {
    final f = await _file(_credentialsFile);
    if (!await f.exists()) return false;
    final j = jsonDecode(await f.readAsString());
    if (j is Map) {
      final t = j['agent_token'];
      final r = j['rustdesk_id'];
      if (t is String && r is String && t.isNotEmpty && r.isNotEmpty) {
        _token = t;
        _rustdeskId = r;
        return true;
      }
    }
  } catch (e) {
    _log('WARN não li a credencial: $e');
  }
  return false;
}

// ---------------------------------------------------------------------------
// Senha
// ---------------------------------------------------------------------------

final Random _rnd = Random.secure();

String _genPassword() {
  // Garante 1 de cada classe exigida + preenche + embaralha (Fisher-Yates),
  // igual ao genPassword() do rotate.go.
  final chars = <String>[
    _pwLower[_rnd.nextInt(_pwLower.length)],
    _pwUpper[_rnd.nextInt(_pwUpper.length)],
    _pwDigits[_rnd.nextInt(_pwDigits.length)],
  ];
  while (chars.length < _pwLen) {
    chars.add(_pwAlphabet[_rnd.nextInt(_pwAlphabet.length)]);
  }
  for (var i = chars.length - 1; i > 0; i--) {
    final j = _rnd.nextInt(i + 1);
    final tmp = chars[i];
    chars[i] = chars[j];
    chars[j] = tmp;
  }
  return chars.join();
}

Future<void> _writePending(String pw) async {
  final f = await _file(_pendingFile);
  await f.writeAsString(jsonEncode({'password': pw}), flush: true);
}

Future<String?> _readPending() async {
  try {
    final f = await _file(_pendingFile);
    if (!await f.exists()) return null;
    final j = jsonDecode(await f.readAsString());
    if (j is Map && j['password'] is String) return j['password'] as String;
  } catch (_) {
    // pendência ilegível: tratada como inexistente
  }
  return null;
}

Future<void> _clearPending() async {
  try {
    final f = await _file(_pendingFile);
    if (await f.exists()) await f.delete();
  } catch (_) {
    // best-effort
  }
}

enum _Reporte { gravado, descartado, falhou }

/// gravado    = o painel guardou a senha.
/// descartado = HTTP 200 com discarded:true. O servidor respondeu mas NÃO
///              guardou: o aparelho não está no cadastro, ou o token não é mais
///              o dele (rotate-device-secret desde 05/09/2026, para matar o laço
///              de retry). Retentar nunca resolve.
/// falhou     = rede ou erro: a pendência segue e o retry reenvia.
Future<_Reporte> _reportRotation(String pw) async {
  try {
    final r = await http
        .post(
          Uri.parse(_rotateUrl),
          headers: _headers,
          body: jsonEncode({
            'rustdesk_id': _rustdeskId,
            'agent_token': _token,
            'password': pw,
          }),
        )
        .timeout(_httpTimeout);
    _log('rotate report -> HTTP ${r.statusCode}');
    if (r.statusCode != 200) return _Reporte.falhou;
    try {
      final j = jsonDecode(r.body);
      if (j is Map && j['discarded'] == true) {
        _log('rotate report DESCARTADO (${j['reason']}) — o painel não tem esta senha');
        return _Reporte.descartado;
      }
    } catch (_) {
      // 200 com corpo ilegível: vale como gravado, como sempre valeu
    }
    return _Reporte.gravado;
  } catch (e) {
    _log('rotate report FALHOU: $e');
    return _Reporte.falhou;
  }
}

/// O servidor recebeu a senha e não guardou. Sem retentar (o token não muda
/// sozinho), a pendência sai — mas o painel ficou SEM a senha deste aparelho.
/// Apagamos a marca da sincronização inicial para ela rodar de novo assim que o
/// presence provar que o aparelho entrou no cadastro. Sem isto, um celular
/// aquietado e adotado semanas depois deixaria o "Conectar" do painel preso em
/// "aguardando o agente", porque a única senha que ele publicou foi descartada.
Future<void> _senhaDescartada() async {
  _cadastrado = false;
  _initialSyncChecked = false;
  await _clearPending();
  try {
    final f = await _file(_pwSyncFlagFile);
    if (await f.exists()) await f.delete();
  } catch (_) {
    // best-effort: sem apagar, a próxima sessão ainda re-sincroniza no 'end'
  }
}

bool _rotating = false; // serializa: dois 'end' seguidos não rodam concorrentes

/// null = a senha NÃO foi aplicada no aparelho (núcleo ainda subindo, sem
/// credencial, ou já rotacionando) — o chamador pode reintentar. Senão, o que
/// o painel fez com o reporte da senha aplicada.
Future<_Reporte?> _rotateNow() async {
  if (_rotating) {
    _log('rotação já em andamento, pulando');
    return null;
  }
  _rotating = true;
  try {
    if (!await _loadCredentials()) {
      _log('ROTATE skip: sem credencial (matrícula pendente?)');
      return null;
    }

    final pw = _genPassword();

    // 1) APLICA ANTES DE REPORTAR. Se falhar, mantém a senha antiga nos dois
    //    lados — consistente e sem lockout — e tenta de novo na próxima sessão.
    bool applied = false;
    try {
      applied = await bind.mainSetPermanentPasswordWithResult(password: pw);
    } catch (e) {
      _log('ROTATE ABORT: setPermanentPassword lançou: $e');
      return null;
    }
    if (!applied) {
      _log('ROTATE ABORT: setPermanentPassword retornou false (senha antiga mantida)');
      return null;
    }

    // 2) a senha nova JÁ está no aparelho -> registra a pendência antes de
    //    reportar, para sobreviver a crash entre aplicar e confirmar.
    try {
      await _writePending(pw);
    } catch (e) {
      _log('ROTATE WARN: não persistiu pendência: $e (seguindo em memória)');
    }

    // 3) reporta; gravado -> limpa. Falhou -> o laço de retry reenvia.
    //    Descartado -> não adianta retentar; espera o aparelho entrar no cadastro.
    final rep = await _reportRotation(pw);
    switch (rep) {
      case _Reporte.gravado:
        await _clearPending();
        _log('ROTATE ok');
        break;
      case _Reporte.descartado:
        await _senhaDescartada();
        break;
      case _Reporte.falhou:
        _log('ROTATE pendente — o retry reenvia');
        break;
    }
    return rep; // aplicada no aparelho; rep diz o que o painel fez com ela
  } finally {
    _rotating = false;
  }
}

/// Reenvia a senha já aplicada no aparelho até o painel confirmar. Enquanto não
/// confirmar, o painel serve a senha ANTIGA e o técnico pode falhar uma vez.
Future<void> _retryPendingRotation() async {
  final pw = await _readPending();
  if (pw == null) return;
  if (!await _loadCredentials()) return;
  switch (await _reportRotation(pw)) {
    case _Reporte.gravado:
      await _clearPending();
      _log('pendência de rotação confirmada');
      break;
    case _Reporte.descartado:
      await _senhaDescartada();
      break;
    case _Reporte.falhou:
      break; // tenta de novo no próximo _retryInterval
  }
}

// ---------------------------------------------------------------------------
// Telemetria de sessão
// ---------------------------------------------------------------------------

/// O que a resposta do session-ingest diz sobre o cadastro deste aparelho.
/// 200 = no cadastro e token aceito. 404 = não está no cadastro. 401 = o token
/// não é mais o dele. Os demais (400, 403, 5xx, rede) não dizem nada sobre o
/// cadastro e não mudam o estado.
void _notarCadastro(int status) {
  if (status == 200) {
    if (_cadastrado == false) {
      _log('aparelho está no cadastro — presence volta a 60s');
    }
    _cadastrado = true;
  } else if (status == 404 || status == 401) {
    if (_cadastrado != false) {
      _log('servidor diz que o aparelho não está no cadastro (HTTP $status) '
          '— presence a cada ${_presenceIntervalSemCadastro.inMinutes} min');
    }
    _cadastrado = false;
  }
}

/// Status HTTP da resposta; 0 se não postou (sem credencial) ou a rede falhou.
Future<int> _postEvent(String event, {String? controllerId}) async {
  // Guarda idêntica ao postEvent do main.go: sem credencial a session-ingest
  // rejeitaria, e postar a cada 60s só geraria ruído.
  if (!await _loadCredentials()) {
    _log('SKIP $event: sem credencial (matrícula pendente?)');
    return 0;
  }
  try {
    final body = <String, String>{
      'rustdesk_id': _rustdeskId!,
      'agent_token': _token!,
      'event': event,
      // Visibilidade de frota, igual ao main.go: pega carona no sinal que ja
      // existe. O servidor grava em address_book.agent_version.
      'agent_version': _agentVersion,
    };
    // Só serve de gatilho de auto-adoção no 'start'; ignorado nos demais.
    if (controllerId != null && controllerId.isNotEmpty) {
      body['controller_rustdesk_id'] = controllerId;
    }
    final r = await http
        .post(Uri.parse(_ingestUrl), headers: _headers, body: jsonEncode(body))
        .timeout(_httpTimeout);
    _log('$event -> HTTP ${r.statusCode}');
    _notarCadastro(r.statusCode);
    return r.statusCode;
  } catch (e) {
    _log('POST $event falhou: $e');
    return 0;
  }
}

/// Só conta como atendimento a conexão ACEITA. No Android o RustDesk põe na
/// lista, com authorized=false, quem ainda espera o "aceitar" na tela — é o que
/// acontece quando a senha não bate. Contá-la abria um atendimento (e cobrança)
/// para uma tentativa que o cliente nunca aceitou, e o 'end' dela girava a
/// senha. Mesma correção do agente Windows ("senha errada girava a senha",
/// 25/08/2026). Se o cliente aceitar, ela vira authorized=true e entra.
bool _emAtendimento(Client c) => !c.disconnected && c.authorized;

int _activeCount() {
  try {
    return gFFI.serverModel.clients.where(_emAtendimento).length;
  } catch (_) {
    return 0;
  }
}

String _controllerId() {
  try {
    final ativos = gFFI.serverModel.clients.where(_emAtendimento);
    if (ativos.isEmpty) return '';
    // peerId é o rustdesk_id de quem conectou (o controlador).
    return ativos.first.peerId.replaceAll(RegExp(r'\D'), '');
  } catch (_) {
    return '';
  }
}

int _prevActive = 0;
// null = ainda não sabemos (nenhuma resposta desde que o app abriu). Ver
// _notarCadastro e _presenceIntervalSemCadastro.
bool? _cadastrado;
DateTime _lastHeartbeat = DateTime.fromMillisecondsSinceEpoch(0);
DateTime _lastPresence = DateTime.fromMillisecondsSinceEpoch(0);
DateTime _lastRetry = DateTime.fromMillisecondsSinceEpoch(0);

// Evita reprocessar o sync inicial a cada tick (a marca definitiva e o arquivo
// em disco; esta flag so poupa I/O dentro do mesmo processo).
bool _initialSyncChecked = false;

/// Sincroniza a senha permanente com o painel UMA VEZ, logo apos a matricula.
///
/// Sem isto, no 1o acesso o painel serve a senha provisionada que o aparelho
/// ainda NAO tem -> a senha nao bate -> o RustDesk cai no caminho de "clique
/// para aceitar" e o tecnico ve "aguarde enquanto o cliente aceita". A rotacao
/// so acontecia no FIM da 1a sessao, por isso "cancelar e tentar de novo"
/// funcionava. Aqui geramos+aplicamos+reportamos a senha ANTES do 1o acesso.
///
/// Roda so com o aparelho ocioso (chamador garante) e marca em disco para nao
/// rotacionar a cada abertura do app — a rotacao de fim de sessao mantem tudo
/// sincronizado dai em diante.
Future<void> _maybeInitialSync() async {
  if (_initialSyncChecked) return;
  // O servidor já disse que o aparelho não está no cadastro: rotacionar agora
  // só geraria outro descarte (e outra chamada a cada tick de 3s). Espera o
  // presence provar a adoção com um 200.
  if (_cadastrado == false) return;
  try {
    final f = await _file(_pwSyncFlagFile);
    if (await f.exists()) {
      _initialSyncChecked = true;
      return;
    }
    // Ainda sem credencial = matricula pendente. Nao marca; tenta no proximo tick.
    if (!await _loadCredentials()) return;

    _log('sincronizacao inicial da senha (1o acesso)');
    final rep = await _rotateNow(); // gera + aplica no aparelho + reporta
    // Nao aplicou (nucleo ainda subindo)? _initialSyncChecked segue false e o
    // proximo tick tenta de novo — sem gravar a marca.
    if (rep == null) return;
    // Descartado: o painel NAO tem a senha. _senhaDescartada ja armou a espera
    // pelo cadastro; gravar a marca aqui travaria o "Conectar" do painel.
    if (rep == _Reporte.descartado) return;
    // Gravado, ou falhou na rede (a pendencia entrega depois): sincronizado.
    _initialSyncChecked = true;
    await f.writeAsString(jsonEncode({'done': true}), flush: true);
  } catch (e) {
    _log('sync inicial da senha falhou: $e (tentara de novo)');
  }
}

Future<void> _tick() async {
  final now = DateTime.now();
  final active = _activeCount();

  // Sincroniza a senha com o painel antes do 1o acesso, so com o aparelho
  // ocioso (nunca no meio de uma sessao).
  if (active == 0) {
    await _maybeInitialSync();
  }

  if (_prevActive == 0 && active > 0) {
    await _postEvent('start', controllerId: _controllerId());
    _lastHeartbeat = now;
  } else if (active > 0) {
    if (now.difference(_lastHeartbeat) >= _heartbeatInterval) {
      await _postEvent('heartbeat');
      _lastHeartbeat = now;
    }
  } else if (_prevActive > 0 && active == 0) {
    await _postEvent('end');
    // Fase 2: a senha que o técnico viu nesta sessão morre aqui.
    await _rotateNow();
    _lastPresence = now;
  } else {
    final intervalo = _cadastrado == false
        ? _presenceIntervalSemCadastro
        : _presenceInterval;
    if (now.difference(_lastPresence) >= intervalo) {
      await _postEvent('presence');
      _lastPresence = now;
    }
    if (now.difference(_lastRetry) >= _retryInterval) {
      await _retryPendingRotation();
      _lastRetry = now;
    }
  }

  _prevActive = active;
}

/// Passa o aparelho a usar SENHA PERMANENTE. Sem isto o app fica na "senha de
/// uso único" que ele mesmo sorteia, e o painel — que serve a senha permanente
/// vinda do device_secrets — nunca casaria com o aparelho.
Future<void> _ensurePermanentPasswordMode() async {
  try {
    await gFFI.serverModel.setVerificationMethod(kUsePermanentPassword);
    _log('modo de verificação: senha permanente');
  } catch (e) {
    _log('WARN não ajustou o modo de verificação: $e');
  }
}

// ---------------------------------------------------------------------------
// Auto-ligar o serviço ao abrir o app (etapa: acesso desassistido)
// ---------------------------------------------------------------------------
// O técnico precisa acessar sem o cliente caçar o botão "Iniciar serviço". Não
// dá para pular o diálogo do sistema ("Iniciar agora" / MediaProjection) — é
// trava do Android, nenhum app contorna — MAS ele é UMA VEZ SÓ: concedido, o
// serviço segue vivo em segundo plano e as reconexões seguintes não pedem nada.
// Então auto-ligamos na abertura; o cliente só interage no 1º start (e após um
// reboot, que zera a permissão de captura). NÃO auto-desligamos: derrubar o
// serviço obrigaria um novo toque a cada atendimento — o oposto do desassistido.

const String _onboardingFlagFile = 'acessofast_onboarding.done';

/// O assistente de primeira abertura (onboarding.dart) grava esta marca quando
/// já rodou. Enquanto ele não rodou, é ELE quem conduz o cliente a ligar o
/// serviço; não auto-ligamos junto para não abrir o diálogo de captura duas
/// vezes nem brigar com o toggleService do assistente (que alterna, não liga).
Future<bool> _onboardingDone() async {
  try {
    final dir = await getApplicationSupportDirectory();
    return await File('${dir.path}/$_onboardingFlagFile').exists();
  } catch (_) {
    return false;
  }
}

/// Liga o serviço automaticamente na abertura. NUNCA bloqueia nem propaga
/// exceção — se o auto-start falhar, o cliente ainda pode ligar pela tela.
void acessofastAutoStartService() {
  unawaited(
    // O delay não é estético: o start dispara o diálogo do sistema, que precisa
    // da interface montada (mesmo motivo do atraso do assistente). 5s deixa o
    // núcleo do RustDesk subir e o assistente aparecer/gravar sua marca antes.
    Future<void>.delayed(const Duration(seconds: 5), () async {
      try {
        if (!isAndroid) return;
        // 1ª abertura: o assistente conduz; não competimos com ele.
        if (!await _onboardingDone()) return;
        // Já ligado (processo/serviço vivo desde um start anterior): nada a fazer.
        if (gFFI.serverModel.isStart) return;
        _log('ligando o serviço automaticamente');
        await gFFI.serverModel.startService();
      } catch (e) {
        _log('auto-start do serviço falhou: $e');
      }
    }),
  );
}

/// Inicia o observador. Não bloqueia e nunca propaga exceção.
void acessofastSessionStart() {
  unawaited(() async {
    try {
      // A primeira sessão só faz sentido depois que a credencial existe; o
      // _postEvent tem guarda própria, então podemos subir o timer já.
      await _ensurePermanentPasswordMode();

      // Se o app caiu entre aplicar e confirmar, resolve na subida.
      await _retryPendingRotation();

      Timer.periodic(_pollInterval, (_) async {
        try {
          await _tick();
        } catch (e) {
          _log('tick falhou: $e');
        }
      });
      _log('observador de sessão ativo');
    } catch (e, s) {
      _log('observador não subiu: $e\n$s');
    }
  }());

  // Auto-liga o serviço na abertura (guarda própria: só depois do assistente e
  // só se ainda não estiver ligado). Fica fora do bloco acima de propósito, para
  // disparar mesmo que o observador tropece ao subir.
  acessofastAutoStartService();
}
