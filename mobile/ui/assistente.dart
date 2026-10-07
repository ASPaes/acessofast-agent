// Assistente de primeira abertura em tela cheia (etapa 2): boas-vindas, os 3 passos e "Tudo
// pronto". Desenho do protótipo aprovado (prototipo-mobile, telaAssistente).
// Copiado pelo CI para flutter/lib/acessofast/ui/.
//
// Mesma lógica do assistente antigo (onboarding.dart), que continua sendo o dono da marca de
// concluído: ele chama esta tela pelo gancho acessofastAssistenteNovo. As ações são as mesmas
// chamadas do gFFI.serverModel; o consentimento da Acessibilidade continua sendo o diálogo já
// aprovado pela Play (o toggleInput passa por ele).
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_hbb/common.dart';
import 'package:flutter_hbb/consts.dart';
import 'package:flutter_hbb/models/platform_model.dart';

import 'comum.dart';
import 'tema.dart';

/// Instalado pela Play o Android não tranca a Acessibilidade ("configurações restritas"); o
/// Beta e o APK direto são instalados fora dela, e lá a chave pode vir acinzentada.
const bool _viaPlayStore = String.fromEnvironment('ACESSOFAST_CANAL') == 'play';

/// Abre o assistente por cima de tudo e espera ele fechar.
Future<void> abrirAssistente() async {
  final nav = globalKey.currentState;
  if (nav == null) return;
  await nav.push(MaterialPageRoute(fullscreenDialog: true, builder: (_) => const AfAssistente()));
}

class AfAssistente extends StatefulWidget {
  const AfAssistente({super.key});

  @override
  State<AfAssistente> createState() => _AfAssistenteState();
}

class _AfAssistenteState extends State<AfAssistente> with WidgetsBindingObserver {
  int _passo = 0; // 0 boas-vindas, 1 notificações, 2 tela, 3 controle, 4 pronto
  bool _aguardando = false; // apertou "Permitir" e esperamos o Android responder
  bool _notificacoes = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    gFFI.serverModel.addListener(_mudou);
    gFFI.serverModel.fetchID();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    gFFI.serverModel.removeListener(_mudou);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) gFFI.serverModel.checkAndroidPermission();
  }

  bool _ok(int passo) {
    final sm = gFFI.serverModel;
    return switch (passo) { 1 => _notificacoes, 2 => sm.isStart && sm.mediaOk, 3 => sm.inputOk, _ => true };
  }

  /// O Android respondeu (captura liberada, Acessibilidade ligada): segue sozinho.
  void _mudou() {
    if (!mounted) return;
    if (_aguardando && _ok(_passo)) {
      setState(() {
        _aguardando = false;
        _passo++;
      });
    } else {
      setState(() {});
    }
  }

  Future<void> _permitir() async {
    final sm = gFFI.serverModel;
    if (_ok(_passo)) return setState(() => _passo++);
    setState(() => _aguardando = true);
    try {
      switch (_passo) {
        case 1:
          await sm.checkRequestNotificationPermission();
          _notificacoes = true;
          _mudou();
        case 2:
          // O que o toggleService fazia antes de ligar, sem o aviso do RustDesk no meio: a
          // janela flutuante (segura a tela ligada no atendimento) e o serviço, que abre a
          // pergunta do Android ("Iniciar agora").
          await sm.checkRequestNotificationPermission();
          if (bind.mainGetLocalOption(key: kOptionDisableFloatingWindow) != 'Y') await sm.checkFloatingWindowPermission();
          if (!sm.isStart || !sm.mediaOk) await sm.startService();
        case 3:
          // Abre o consentimento aprovado pela Play e, no "Concordo", a Acessibilidade.
          await sm.toggleInput();
      }
    } catch (e) {
      debugPrint('assistente: $e');
      if (mounted) setState(() => _aguardando = false);
    }
  }

  void _pular() => setState(() {
        _aguardando = false;
        _passo++;
      });

  @override
  Widget build(BuildContext context) {
    final c = AfCores.of(context);
    return WillPopScope(
      onWillPop: () async => _passo == 4,
      child: Scaffold(
        backgroundColor: c.bg,
        body: Container(
          decoration: BoxDecoration(
            gradient: RadialGradient(center: const Alignment(0, -1.3), radius: 0.9, colors: [c.glow, c.bg], stops: const [0, 0.75]),
          ),
          child: SafeArea(
            child: Padding(
              padding: const EdgeInsets.fromLTRB(22, 10, 22, 22),
              child: switch (_passo) { 0 => _boasVindas(c), 4 => _pronto(c), _ => _etapa(c) },
            ),
          ),
        ),
      ),
    );
  }

  Widget _boasVindas(AfCores c) {
    Widget item(String t) => Padding(
          padding: const EdgeInsets.symmetric(vertical: 5),
          child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Icon(Icons.check_rounded, size: 18, color: c.ok),
            const SizedBox(width: 10),
            Expanded(child: Text(t, style: TextStyle(fontFamily: kFonte, fontSize: 13.5, height: 1.45, color: c.text2))),
          ]),
        );
    return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      const Spacer(),
      Center(
        child: ClipRRect(
          borderRadius: BorderRadius.circular(18),
          child: Image.asset('assets/acessofast/icon.png', width: 72, height: 72, errorBuilder: (_, __, ___) => const SizedBox(width: 72, height: 72)),
        ),
      ),
      const SizedBox(height: 18),
      _titulo(c, 'Bem-vindo ao AcessoFast', centro: true),
      const SizedBox(height: 10),
      _texto(c, 'Receba suporte no seu celular. Se você é técnico, acesse daqui os computadores da sua empresa.', centro: true),
      const SizedBox(height: 18),
      AfCartao(
        child: Column(children: [
          item('Só os técnicos da sua empresa, ou quem você aprovar na hora, conseguem conectar.'),
          item('Você vê o tempo todo quando alguém está conectado.'),
          item('Você encerra o acesso quando quiser, com um toque.'),
        ]),
      ),
      const Spacer(),
      AfBotao('Começar', estilo: AfEstiloBotao.primario, largo: true, onTap: () => setState(() => _passo = 1)),
    ]);
  }

  Widget _etapa(AfCores c) {
    final (IconData icone, String titulo, String texto) = switch (_passo) {
      1 => (
          Icons.notifications_none_rounded,
          'Avisar quando alguém conectar',
          'Enquanto um técnico estiver conectado, aparece um aviso fixo no topo do celular. Assim você sempre sabe que tem alguém ali.'
        ),
      2 => (
          Icons.cast_rounded,
          'Deixar o técnico ver a tela',
          'O Android vai pedir uma confirmação. Toque em "Iniciar agora". Isso só é usado quando você está sendo atendido.'
        ),
      _ => (
          Icons.touch_app_outlined,
          'Deixar o técnico tocar na tela',
          'Sem isto o técnico só consegue olhar. Com isto ele toca, desliza e digita por você, sem precisar te pedir passo a passo.'
        ),
    };
    final ok = _ok(_passo);
    return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      Row(children: [
        for (var i = 1; i <= 3; i++) ...[
          if (i > 1) const SizedBox(width: 6),
          Expanded(
            child: Container(height: 4, decoration: BoxDecoration(color: i <= _passo ? c.primary : c.line2, borderRadius: BorderRadius.circular(4))),
          ),
        ],
      ]),
      const SizedBox(height: 14),
      Text('Passo $_passo de 3', style: TextStyle(fontFamily: kFonte, fontSize: 12.5, fontWeight: FontWeight.w700, color: c.muted)),
      const SizedBox(height: 18),
      Center(child: _arte(c, ok ? Icons.check_rounded : icone, ok ? c.ok : c.accentText)),
      const SizedBox(height: 22),
      _titulo(c, titulo),
      const SizedBox(height: 10),
      _texto(c, texto),
      if (_passo == 3 && !_viaPlayStore) ...[
        const SizedBox(height: 16),
        AfCartao(
          padding: const EdgeInsets.all(14),
          child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Icon(Icons.info_outline_rounded, size: 18, color: c.warn),
            const SizedBox(width: 10),
            Expanded(
              child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                const AfMiudo('A opção "AcessoFast Input" aparece acinzentada? Antes, libere em Informações do aplicativo › ⋮ › '
                    '"Permitir configurações restritas".'),
                const SizedBox(height: 8),
                AfBotao('Abrir Informações do aplicativo', pequeno: true, onTap: () {
                  try {
                    AndroidPermissionManager.startAction('android.settings.APPLICATION_DETAILS_SETTINGS');
                  } catch (e) {
                    debugPrint('assistente: $e');
                  }
                }),
              ]),
            ),
          ]),
        ),
      ],
      const Spacer(),
      if (_aguardando && !ok)
        Padding(
          padding: const EdgeInsets.only(bottom: 12),
          child: Center(child: AfMiudo('Esperando a confirmação do Android…', alinhar: TextAlign.center)),
        ),
      AfBotao(ok ? 'Continuar' : 'Permitir', estilo: AfEstiloBotao.primario, largo: true, onTap: _permitir),
      const SizedBox(height: 6),
      if (!ok) AfBotao('Agora não', estilo: AfEstiloBotao.texto, largo: true, onTap: _pular),
    ]);
  }

  Widget _pronto(AfCores c) {
    final sm = gFFI.serverModel;
    final tudo = sm.isStart && sm.mediaOk && sm.inputOk;
    return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      Row(children: [
        for (var i = 1; i <= 3; i++) ...[
          if (i > 1) const SizedBox(width: 6),
          Expanded(child: Container(height: 4, decoration: BoxDecoration(color: c.primary, borderRadius: BorderRadius.circular(4)))),
        ],
      ]),
      const SizedBox(height: 26),
      Center(child: _arte(c, tudo ? Icons.check_rounded : Icons.info_outline_rounded, tudo ? c.ok : c.warn)),
      const SizedBox(height: 22),
      _titulo(c, tudo ? 'Tudo pronto' : 'Quase pronto', centro: true),
      const SizedBox(height: 10),
      _texto(
          c,
          tudo
              ? 'Quando precisar de suporte, passe este ID ao técnico.'
              : 'Dá para receber acesso, mas falta liberar uma permissão. Você resolve depois na aba Este celular.',
          centro: true),
      const SizedBox(height: 18),
      AfCartao(
        child: Column(children: [
          const AfRotulo('ID deste celular'),
          const SizedBox(height: 6),
          Center(child: AfIdGrande(sm.serverId.text)),
        ]),
      ),
      const SizedBox(height: 12),
      AfCartao(
        child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Icon(Icons.battery_charging_full_rounded, size: 20, color: c.warn),
          const SizedBox(width: 12),
          const Expanded(
            child: AfMiudo('Dica: em Configurações › Bateria, deixe o AcessoFast como "Sem restrições". '
                'Alguns celulares fecham o app sozinhos e o acesso cai.'),
          ),
        ]),
      ),
      const Spacer(),
      AfBotao('Ir para o app', estilo: AfEstiloBotao.primario, largo: true, onTap: () => Navigator.of(context).pop()),
    ]);
  }

  Widget _arte(AfCores c, IconData icone, Color cor) => Container(
        width: 120,
        height: 120,
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(34),
          border: Border.all(color: cor.withOpacity(0.3)),
          gradient: RadialGradient(center: const Alignment(-0.4, -0.6), colors: [cor.withOpacity(0.3), cor.withOpacity(0.08)]),
        ),
        child: Icon(icone, size: 52, color: cor),
      );

  Widget _titulo(AfCores c, String t, {bool centro = false}) => Text(t,
      textAlign: centro ? TextAlign.center : TextAlign.start,
      style: TextStyle(fontFamily: kFonte, fontSize: 26, fontWeight: FontWeight.w800, letterSpacing: -0.5, height: 1.15, color: c.text));

  Widget _texto(AfCores c, String t, {bool centro = false}) => Text(t,
      textAlign: centro ? TextAlign.center : TextAlign.start,
      style: TextStyle(fontFamily: kFonte, fontSize: 14.5, height: 1.5, color: c.text2));
}
