// AcessoFast — agente embutido (Android). Porte do matricula.go.
//
// Este arquivo é COPIADO pelo CI para flutter/lib/acessofast/agent.dart e
// chamado de runMobileApp() no main.dart do RustDesk.
//
// ARQUIVO NOVO em vez de sed espalhado, de propósito: o build parte de uma tag
// do rustdesk/rustdesk. Patch por sed quebra a cada upgrade de tag; um arquivo
// próprio só depende de duas âncoras (o import e a chamada), que o CI verifica.
//
// ---------------------------------------------------------------------------
// O QUE ESTE MÓDULO FAZ (etapa 5 do MOBILE-DESIGN.md)
// ---------------------------------------------------------------------------
// Matrícula por nonce — MESMO protocolo do agente Windows, mesmos endpoints,
// nenhuma edge function nova:
//
//   1. Espera o RustDesk ID existir (o núcleo pode ainda estar subindo).
//   2. Gera nonce + token e PERSISTE. Restart não cria pedido novo — o hash do
//      token precisa continuar batendo com o que foi registrado no claim.
//   3. claim-register: cria o pedido de adoção pendente (só hashes; o servidor
//      nunca vê nonce nem token em claro).
//   4. claim-status em laço, provando o nonce:
//        waiting                      -> continua esperando
//        approved | consumed          -> grava a credencial, matrícula concluída
//        expired | rejected | unknown -> re-registra
//
// DIFERENÇAS FRENTE AO WINDOWS (e por quê):
//   - Sem ACL por SID. O diretório privado do app no Android já é isolado por
//     UID pelo próprio SO — outro app não lê. Equivalente ou melhor, e sem
//     código de permissão para manter.
//   - Sem `--get-id` por linha de comando: o ID vem de bind.mainGetMyId(),
//     dentro do mesmo processo.
//
// REGRA DE OURO: este módulo NUNCA pode derrubar o app. Toda falha é engolida e
// logada. Um erro de rede na matrícula não pode impedir alguém de usar o
// aparelho — a matrícula tenta de novo sozinha.

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'package:crypto/crypto.dart';
import 'package:flutter_hbb/models/platform_model.dart';
import 'package:http/http.dart' as http;
import 'package:path_provider/path_provider.dart';

const String _fnBase = 'https://plmfyibyrowbgjjyblcl.supabase.co/functions/v1';
const String _claimRegisterUrl = '$_fnBase/claim-register';
const String _claimStatusUrl = '$_fnBase/claim-status';

// anon key é pública por design (role=anon). Mesma do agente Windows.
// As funções de claim autenticam por nonce/hash, não por esta chave.
const String _anonKey =
    'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6InBsbWZ5aWJ5cm93YmdqanlibGNsIiwicm9sZSI6ImFub24iLCJpYXQiOjE3ODM2NDMyNjIsImV4cCI6MjA5OTIxOTI2Mn0.grcQYqN3fHvFTWI0AFPWG66k1wONuGqZ5yMt07qcjxE';

const Duration _pollInterval = Duration(seconds: 15);
const Duration _httpTimeout = Duration(seconds: 20);

const String _enrollStateFile = 'acessofast_enroll.state';
const String _credentialsFile = 'acessofast_agent.json';

void _log(String msg) {
  // print vira logcat no Android; prefixo fixo para filtrar com `adb logcat`.
  // ignore: avoid_print
  print('[acessofast] $msg');
}

/// Estado da matrícula. Persistido para sobreviver a restart: mesmo nonce+token
/// -> mesmo pedido -> o hash adotado bate com o token final.
class _EnrollState {
  final String nonce; // prova de posse; só sai daqui no poll (sob TLS)
  final String token; // vira a credencial do agente; só o hash é registrado

  const _EnrollState(this.nonce, this.token);

  Map<String, dynamic> toJson() => {'nonce': nonce, 'token': token};

  static _EnrollState? fromJson(Map<String, dynamic> j) {
    final n = j['nonce'];
    final t = j['token'];
    if (n is String && t is String && n.isNotEmpty && t.isNotEmpty) {
      return _EnrollState(n, t);
    }
    return null;
  }
}

/// 32 bytes aleatórios em base64url SEM padding — idêntico ao
/// base64.RawURLEncoding do Go, para que os hashes batam com o servidor.
String _randB64Url(int n) {
  final rnd = Random.secure();
  final bytes = List<int>.generate(n, (_) => rnd.nextInt(256));
  return base64Url.encode(bytes).replaceAll('=', '');
}

String _sha256Hex(String s) => sha256.convert(utf8.encode(s)).toString();

Future<File> _stateFile(String name) async {
  final dir = await getApplicationSupportDirectory();
  return File('${dir.path}/$name');
}

Future<_EnrollState> _loadOrCreateState() async {
  try {
    final f = await _stateFile(_enrollStateFile);
    if (await f.exists()) {
      final parsed = jsonDecode(await f.readAsString());
      if (parsed is Map<String, dynamic>) {
        final st = _EnrollState.fromJson(parsed);
        if (st != null) {
          _log('estado de matrícula recuperado do disco');
          return st;
        }
      }
    }
  } catch (e) {
    _log('WARN não consegui ler enroll.state: $e (gerando novo)');
  }

  final st = _EnrollState(_randB64Url(32), _randB64Url(32));
  try {
    final f = await _stateFile(_enrollStateFile);
    await f.writeAsString(jsonEncode(st.toJson()), flush: true);
  } catch (e) {
    // Segue em memória: um restart recria o pedido, o que é recuperável.
    _log('WARN não persistiu enroll.state: $e (seguindo em memória)');
  }
  return st;
}

Future<void> _writeCredentials(String token, String rustdeskId) async {
  final f = await _stateFile(_credentialsFile);
  await f.writeAsString(
    jsonEncode({'agent_token': token, 'rustdesk_id': rustdeskId}),
    flush: true,
  );
}

Future<void> _clearEnrollState() async {
  try {
    final f = await _stateFile(_enrollStateFile);
    if (await f.exists()) await f.delete();
  } catch (_) {
    // irrelevante: o arquivo perde a função depois da adoção
  }
}

Map<String, String> get _headers => {
      'content-type': 'application/json',
      'apikey': _anonKey,
      'authorization': 'Bearer $_anonKey',
    };

/// O ID só existe depois que o núcleo sobe. Repete até aparecer.
Future<String> _waitForRustDeskId() async {
  while (true) {
    try {
      final id = (await bind.mainGetMyId()).replaceAll(RegExp(r'\D'), '');
      // O servidor valida 6..12 dígitos (RUSTID_RE); abaixo disso é placeholder.
      if (id.length >= 6) return id;
    } catch (e) {
      _log('WARN mainGetMyId falhou: $e');
    }
    _log('aguardando o ID deste aparelho...');
    await Future<void>.delayed(_pollInterval);
  }
}

String _osString() {
  try {
    return 'Android ${Platform.operatingSystemVersion}';
  } catch (_) {
    return 'Android';
  }
}

String _hostname() {
  try {
    final h = Platform.localHostname;
    if (h.isNotEmpty && h != 'localhost') return h;
  } catch (_) {
    // cai no rótulo genérico
  }
  return 'Android';
}

Future<void> _postClaimRegister(
  String rid,
  String nonceHash,
  String tokenHash,
  String host,
  String os,
) async {
  try {
    final r = await http
        .post(
          Uri.parse(_claimRegisterUrl),
          headers: _headers,
          body: jsonEncode({
            'rustdesk_id': rid,
            'nonce_hash': nonceHash,
            'agent_token_hash': tokenHash,
            'hostname': host,
            'os': os,
          }),
        )
        .timeout(_httpTimeout);
    _log('claim-register -> HTTP ${r.statusCode}');
  } catch (e) {
    // Sem re-throw: o laço de status re-registra quando o pedido não existir.
    _log('claim-register FALHOU: $e');
  }
}

/// '' em erro de rede; senão waiting|approved|consumed|expired|rejected|unknown.
Future<String> _postClaimStatus(String rid, String nonce) async {
  try {
    final r = await http
        .post(
          Uri.parse(_claimStatusUrl),
          headers: _headers,
          body: jsonEncode({'rustdesk_id': rid, 'nonce': nonce}),
        )
        .timeout(_httpTimeout);
    final body = jsonDecode(r.body);
    if (body is Map && body['status'] is String) return body['status'] as String;
  } catch (_) {
    // rede instável é normal em celular; o laço tenta de novo
  }
  return '';
}

Future<bool> _alreadyEnrolled() async {
  try {
    final f = await _stateFile(_credentialsFile);
    if (!await f.exists()) return false;
    final parsed = jsonDecode(await f.readAsString());
    return parsed is Map && (parsed['agent_token'] as String?)?.isNotEmpty == true;
  } catch (_) {
    return false;
  }
}

Future<void> _runEnrollment() async {
  if (await _alreadyEnrolled()) {
    _log('já matriculado — nada a fazer');
    return;
  }

  final rid = await _waitForRustDeskId();
  _log('rustdesk_id = $rid');

  final st = await _loadOrCreateState();
  final nonceHash = _sha256Hex(st.nonce);
  final tokenHash = _sha256Hex(st.token);
  final host = _hostname();
  final os = _osString();

  await _postClaimRegister(rid, nonceHash, tokenHash, host, os);
  _log('claim registrado; aguardando adoção pelo painel');

  while (true) {
    await Future<void>.delayed(_pollInterval);

    switch (await _postClaimStatus(rid, st.nonce)) {
      case 'approved':
      case 'consumed':
        try {
          await _writeCredentials(st.token, rid);
          await _clearEnrollState();
          _log('ADOTADO — credencial persistida; matrícula concluída');
          return;
        } catch (e) {
          _log('ERRO ao gravar credencial: $e (retentando)');
        }
        break;
      case 'waiting':
        break; // segue esperando
      case 'expired':
      case 'rejected':
      case 'unknown':
        _log('pedido morto/sumido -> re-registrando');
        await _postClaimRegister(rid, nonceHash, tokenHash, host, os);
        break;
      default:
        // '' = erro de rede. Não re-registra: evita duplicar pedido por queda
        // momentânea de sinal, que em celular é o caso comum.
        break;
    }
  }
}

/// Ponto de entrada. Chamado de runMobileApp() no main.dart.
///
/// NÃO bloqueia e NÃO propaga exceção: o app tem que abrir normalmente mesmo
/// sem rede, sem servidor, ou com o núcleo do RustDesk ainda subindo.
void acessofastAgentStart() {
  unawaited(() async {
    try {
      await _runEnrollment();
    } catch (e, s) {
      _log('agente parou por exceção não tratada: $e\n$s');
    }
  }());
}
