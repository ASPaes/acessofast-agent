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
// Transições (idêntico ao main.go):
//   0 -> N ativos : 'start'   (+ controller_rustdesk_id, dispara auto-adoção)
//   N ativos      : 'heartbeat' a cada 20s
//   N -> 0        : 'end'     + ROTAÇÃO da senha
//   ocioso        : 'presence' a cada 60s
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

// Mesmos intervalos do main.go.
const Duration _pollInterval = Duration(seconds: 3);
const Duration _heartbeatInterval = Duration(seconds: 20);
const Duration _presenceInterval = Duration(seconds: 60);
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

/// true SÓ em HTTP 200 — o painel confirmou que gravou.
Future<bool> _reportRotation(String pw) async {
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
    return r.statusCode == 200;
  } catch (e) {
    _log('rotate report FALHOU: $e');
    return false;
  }
}

bool _rotating = false; // serializa: dois 'end' seguidos não rodam concorrentes

Future<void> _rotateNow() async {
  if (_rotating) {
    _log('rotação já em andamento, pulando');
    return;
  }
  _rotating = true;
  try {
    if (!await _loadCredentials()) {
      _log('ROTATE skip: sem credencial (matrícula pendente?)');
      return;
    }

    final pw = _genPassword();

    // 1) APLICA ANTES DE REPORTAR. Se falhar, mantém a senha antiga nos dois
    //    lados — consistente e sem lockout — e tenta de novo na próxima sessão.
    bool applied = false;
    try {
      applied = await bind.mainSetPermanentPasswordWithResult(password: pw);
    } catch (e) {
      _log('ROTATE ABORT: setPermanentPassword lançou: $e');
      return;
    }
    if (!applied) {
      _log('ROTATE ABORT: setPermanentPassword retornou false (senha antiga mantida)');
      return;
    }

    // 2) a senha nova JÁ está no aparelho -> registra a pendência antes de
    //    reportar, para sobreviver a crash entre aplicar e confirmar.
    try {
      await _writePending(pw);
    } catch (e) {
      _log('ROTATE WARN: não persistiu pendência: $e (seguindo em memória)');
    }

    // 3) reporta; sucesso -> limpa. Falha -> o laço de retry reenvia.
    if (await _reportRotation(pw)) {
      await _clearPending();
      _log('ROTATE ok');
    } else {
      _log('ROTATE pendente — o retry reenvia');
    }
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
  if (await _reportRotation(pw)) {
    await _clearPending();
    _log('pendência de rotação confirmada');
  }
}

// ---------------------------------------------------------------------------
// Telemetria de sessão
// ---------------------------------------------------------------------------

Future<void> _postEvent(String event, {String? controllerId}) async {
  // Guarda idêntica ao postEvent do main.go: sem credencial a session-ingest
  // rejeitaria, e postar a cada 60s só geraria ruído.
  if (!await _loadCredentials()) {
    _log('SKIP $event: sem credencial (matrícula pendente?)');
    return;
  }
  try {
    final body = <String, String>{
      'rustdesk_id': _rustdeskId!,
      'agent_token': _token!,
      'event': event,
    };
    // Só serve de gatilho de auto-adoção no 'start'; ignorado nos demais.
    if (controllerId != null && controllerId.isNotEmpty) {
      body['controller_rustdesk_id'] = controllerId;
    }
    final r = await http
        .post(Uri.parse(_ingestUrl), headers: _headers, body: jsonEncode(body))
        .timeout(_httpTimeout);
    _log('$event -> HTTP ${r.statusCode}');
  } catch (e) {
    _log('POST $event falhou: $e');
  }
}

int _activeCount() {
  try {
    return gFFI.serverModel.clients.where((c) => !c.disconnected).length;
  } catch (_) {
    return 0;
  }
}

String _controllerId() {
  try {
    final ativos = gFFI.serverModel.clients.where((c) => !c.disconnected);
    if (ativos.isEmpty) return '';
    // peerId é o rustdesk_id de quem conectou (o controlador).
    return ativos.first.peerId.replaceAll(RegExp(r'\D'), '');
  } catch (_) {
    return '';
  }
}

int _prevActive = 0;
DateTime _lastHeartbeat = DateTime.fromMillisecondsSinceEpoch(0);
DateTime _lastPresence = DateTime.fromMillisecondsSinceEpoch(0);
DateTime _lastRetry = DateTime.fromMillisecondsSinceEpoch(0);

Future<void> _tick() async {
  final now = DateTime.now();
  final active = _activeCount();

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
    if (now.difference(_lastPresence) >= _presenceInterval) {
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
}
