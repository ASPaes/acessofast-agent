// "Quem acessou este celular" e o aviso "O AcessoFast foi atualizado" (etapa 8).
// Copiado pelo CI para flutter/lib/acessofast/ui/.
//
// Quem acessou é um REGISTRO LOCAL, como no desktop: o próprio app anota cada acesso recebido.
// Funciona sem login, que é o caso do cliente final, e não depende do painel (pelas regras de
// acesso do banco, o técnico só enxerga as próprias sessões).
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_hbb/common.dart' hide Dialog;
import 'package:flutter_hbb/models/platform_model.dart';
import 'package:path_provider/path_provider.dart';

import 'celular.dart';
import 'comum.dart';
import 'tema.dart';

/// Versão deste build, a mesma que o agente reporta ao painel (AAAA.MM.DD-commit).
const versaoDoApp = String.fromEnvironment('ACESSOFAST_VERSION', defaultValue: 'dev');

class AcessoRecebido {
  final String nome;
  final String peerId;
  final DateTime inicio;
  DateTime? fim;
  AcessoRecebido(this.nome, this.peerId, this.inicio, [this.fim]);

  Map<String, dynamic> toJson() => {'n': nome, 'p': peerId, 'i': inicio.toIso8601String(), if (fim != null) 'f': fim!.toIso8601String()};
  static AcessoRecebido? fromJson(Map<String, dynamic> m) {
    final i = DateTime.tryParse('${m['i']}');
    if (i == null) return null;
    return AcessoRecebido('${m['n'] ?? ''}', '${m['p'] ?? ''}', i, DateTime.tryParse('${m['f'] ?? ''}'));
  }
}

class RegistroAcessos extends ChangeNotifier {
  RegistroAcessos._();
  static final instancia = RegistroAcessos._();
  static const _maximo = 30;

  final List<AcessoRecebido> acessos = [];
  final Map<int, AcessoRecebido> _abertos = {};
  bool _ligado = false;

  Future<File> _arquivo() async => File('${(await getApplicationSupportDirectory()).path}/acessofast_acessos.json');

  /// Lê o que já foi anotado e passa a observar as conexões do ServerModel.
  Future<void> ligar() async {
    if (_ligado) return;
    _ligado = true;
    try {
      final f = await _arquivo();
      if (await f.exists()) {
        final l = jsonDecode(await f.readAsString()) as List;
        acessos
          ..clear()
          ..addAll(l.cast<Map<String, dynamic>>().map(AcessoRecebido.fromJson).whereType<AcessoRecebido>());
      }
    } catch (e) {
      debugPrint('registro de acessos: $e');
    }
    gFFI.serverModel.addListener(_observar);
    _observar();
    notifyListeners();
  }

  void _observar() {
    final ativos = afAtendimentos(gFFI.serverModel);
    var mudou = false;
    for (final cl in ativos) {
      if (_abertos.containsKey(cl.id)) continue;
      final a = AcessoRecebido(cl.name.trim(), cl.peerId, DateTime.now());
      _abertos[cl.id] = a;
      acessos.insert(0, a);
      mudou = true;
    }
    for (final id in _abertos.keys.toList()) {
      if (ativos.any((c) => c.id == id)) continue;
      _abertos.remove(id)!.fim = DateTime.now();
      mudou = true;
    }
    if (!mudou) return;
    if (acessos.length > _maximo) acessos.removeRange(_maximo, acessos.length);
    notifyListeners();
    _gravar();
  }

  Future<void> _gravar() async {
    try {
      await (await _arquivo()).writeAsString(jsonEncode(acessos.map((a) => a.toJson()).toList()), flush: true);
    } catch (e) {
      debugPrint('registro de acessos: $e');
    }
  }
}

String _quando(DateTime t) {
  final agora = DateTime.now();
  final hoje = DateTime(agora.year, agora.month, agora.day);
  final dia = DateTime(t.year, t.month, t.day);
  final hora = '${t.hour.toString().padLeft(2, '0')}:${t.minute.toString().padLeft(2, '0')}';
  if (dia == hoje) return 'hoje, $hora';
  if (dia == hoje.subtract(const Duration(days: 1))) return 'ontem, $hora';
  return '${t.day.toString().padLeft(2, '0')}/${t.month.toString().padLeft(2, '0')}, $hora';
}

String _duracao(AcessoRecebido a) {
  if (a.fim == null) return 'agora';
  final m = a.fim!.difference(a.inicio).inMinutes;
  if (m < 1) return 'menos de 1 min';
  if (m < 60) return '$m min';
  return '${m ~/ 60} h ${m % 60} min';
}

/// Lista "Quem acessou este celular", para a tela Este celular.
class AfQuemAcessou extends StatelessWidget {
  const AfQuemAcessou({super.key});

  @override
  Widget build(BuildContext context) {
    final c = AfCores.of(context);
    return AnimatedBuilder(
      animation: RegistroAcessos.instancia,
      builder: (context, _) {
        final l = RegistroAcessos.instancia.acessos.take(10).toList();
        return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
          const AfSecao('Quem acessou este celular', direita: 'neste aparelho'),
          const SizedBox(height: 8),
          if (l.isEmpty)
            const AfCartao(child: AfMiudo('Ninguém acessou este celular ainda. Cada acesso recebido aparece aqui.'))
          else
            AfLista([
              for (final a in l)
                AfLinha(
                  icone: Icons.person_outline_rounded,
                  titulo: a.nome.isNotEmpty ? a.nome : 'ID ${a.peerId}',
                  sub: a.nome.isNotEmpty ? 'ID ${a.peerId}' : null,
                  fim: Text('${_quando(a.inicio)}\n${_duracao(a)}',
                      textAlign: TextAlign.right, style: TextStyle(fontFamily: kFonte, fontSize: 12, height: 1.4, color: c.muted)),
                ),
            ]),
        ]);
      },
    );
  }
}

// ==================================================================== aviso de atualização

/// Novidades do app no celular, mostradas UMA vez depois de cada atualização pela Play. Mesma
/// regra do novidades_app.dart do desktop: cada versão que muda algo visível ganha uma entrada no
/// TOPO, com texto para quem usa (o que mudou e onde achar). A lista é própria do celular.
class NovidadeApp {
  final String data; // AAAA.MM.DD
  final List<String> itens;
  const NovidadeApp(this.data, this.itens);
}

const novidadesCelular = <NovidadeApp>[];

/// Para quem vem do AcessoFast de antes (primeira abertura com a interface nova).
const boasVindasCelular = <String>[
  'Visual novo, com tema claro e escuro.',
  'Em Este celular você vê o ID, as permissões e quem acessou este celular.',
  'Os computadores da empresa aparecem em Acessar: entre com a conta do painel.',
  'Cliente, CNPJ e marcadores em cada computador, com busca por nome, ID, cliente ou CNPJ.',
];

List<String> novidadesDesde(String anterior, String atual) {
  final de = anterior.split('-').first;
  final ate = atual.split('-').first;
  return [
    for (final n in novidadesCelular)
      if (n.data.compareTo(de) > 0 && n.data.compareTo(ate) <= 0) ...n.itens,
  ];
}

/// O assistente de permissões (onboarding.dart) grava esta marca ao terminar: existir quer dizer
/// que o app já era usado neste celular antes desta versão.
Future<bool> _jaUsavaOApp() async {
  try {
    return await File('${(await getApplicationSupportDirectory()).path}/acessofast_onboarding.done').exists();
  } catch (_) {
    return false;
  }
}

/// Mostra UMA vez por versão o que mudou. Na primeira vez com a interface nova, apresenta o app.
Future<void> avisarAtualizacao(BuildContext context) async {
  const chave = 'acessofast-versao-vista';
  if (versaoDoApp == 'dev') return;
  final vista = bind.mainGetLocalOption(key: chave);
  if (vista == versaoDoApp) return;
  await bind.mainSetLocalOption(key: chave, value: versaoDoApp);
  final primeira = vista.isEmpty;
  // Instalação nova (o assistente nunca rodou): quem apresenta o app é o assistente.
  if (primeira && !await _jaUsavaOApp()) return;
  final itens = primeira ? boasVindasCelular : novidadesDesde(vista, versaoDoApp);
  // Versão nova sem nada visível para contar: não interrompe ninguém.
  if (!primeira && itens.isEmpty) return;
  if (!context.mounted) return;
  final c = AfCores.of(context);
  await showDialog(
    context: context,
    builder: (ctx) => Dialog(
      backgroundColor: c.side,
      insetPadding: const EdgeInsets.all(22),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(24), side: BorderSide(color: c.line2)),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 22, 20, 18),
        child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start, children: [
          Text(primeira ? 'Bem-vindo ao novo AcessoFast' : 'O AcessoFast foi atualizado',
              style: TextStyle(fontFamily: kFonte, fontSize: 19, fontWeight: FontWeight.w800, color: c.text)),
          const SizedBox(height: 8),
          Text(primeira ? 'O AcessoFast deste celular ganhou um visual novo. O ID continua o mesmo.' : 'Versão $versaoDoApp. O que mudou:',
              style: TextStyle(fontFamily: kFonte, fontSize: 13.5, height: 1.45, color: c.text2)),
          const SizedBox(height: 12),
          for (final i in itens)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
                Padding(padding: const EdgeInsets.only(top: 2), child: Icon(Icons.check_rounded, size: 16, color: c.ok)),
                const SizedBox(width: 9),
                Expanded(child: Text(i, style: TextStyle(fontFamily: kFonte, fontSize: 13, height: 1.4, color: c.text2))),
              ]),
            ),
          if (primeira) Text('versão $versaoDoApp', style: TextStyle(fontFamily: kFonteMono, fontSize: 11, color: c.muted)),
          const SizedBox(height: 14),
          AfBotao('Entendi', estilo: AfEstiloBotao.primario, largo: true, onTap: () => Navigator.of(ctx).pop()),
        ]),
      ),
    ),
  );
}

/// Pacote do build (--dart-define do CI). O app-name do custom é "AcessoFast" no Beta também,
/// então é pelo pacote que se sabe se este é o Beta.
const _pacote = String.fromEnvironment('ACESSOFAST_PACOTE');

/// "versão X", com "beta" só no Beta, como o rodapé do desktop.
String textoVersao() => 'versão $versaoDoApp${_pacote.endsWith('.beta') ? ' beta' : ''}';
