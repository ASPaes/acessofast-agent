// Casca do app no celular: barra de baixo com Acessar, Este celular e Ajustes, e a faixa de
// atendimento em andamento. Desenho do protótipo aprovado (prototipo-mobile).
// Copiado pelo CI para flutter/lib/acessofast/ui/ e montado pela HomePage do RustDesk.
//
// Também liga o que vale para o app inteiro: o assistente em tela cheia (gancho do
// onboarding.dart), o registro de quem acessou e o aviso de atualização.
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_hbb/common.dart';
import 'package:flutter_hbb/common/widgets/chat_page.dart';
import 'package:flutter_hbb/models/chat_model.dart';
import 'package:flutter_hbb/models/platform_model.dart';
import 'package:flutter_hbb/models/server_model.dart';
import 'package:provider/provider.dart';

import '../onboarding.dart';
import 'acessar.dart';
import 'ajustes.dart';
import 'assistente.dart';
import 'celular.dart';
import 'comum.dart';
import 'registro.dart';
import 'tema.dart';

class AfInicioMobile extends StatefulWidget {
  const AfInicioMobile({super.key});

  @override
  State<AfInicioMobile> createState() => _AfInicioMobileState();
}

class _AfInicioMobileState extends State<AfInicioMobile> {
  // Abre em "Este celular": é o que o cliente final procura. O técnico troca com um toque.
  int _aba = 1;

  @override
  void initState() {
    super.initState();
    // O agent.dart abre o assistente 4 s depois de subir; com o gancho preenchido, ele vem na
    // tela cheia nova em vez do diálogo antigo.
    acessofastAssistenteNovo = abrirAssistente;
    unawaited(RegistroAcessos.instancia.ligar());
    // Depois do assistente ter chance de abrir (ver avisarAtualizacao).
    Future.delayed(const Duration(milliseconds: 1500), () {
      if (mounted) unawaited(avisarAtualizacao(context));
    });
  }

  @override
  Widget build(BuildContext context) {
    final c = AfCores.of(context);
    const paginas = [AfAcessar(), AfEsteCelular(), AfAjustes()];
    return WillPopScope(
      onWillPop: () async {
        if (_aba != 1) {
          setState(() => _aba = 1);
          return false;
        }
        return true;
      },
      child: ChangeNotifierProvider.value(
        value: gFFI.serverModel,
        child: Scaffold(
          backgroundColor: c.bg,
          body: SafeArea(
            bottom: false,
            child: Container(
              // Brilho azul no canto de cima, como o .scroll do protótipo.
              decoration: BoxDecoration(
                gradient: RadialGradient(center: const Alignment(1.1, -1.25), radius: 0.9, colors: [c.glow, c.bg], stops: const [0, 0.7]),
              ),
              child: IndexedStack(index: _aba, children: paginas),
            ),
          ),
          bottomNavigationBar: Column(mainAxisSize: MainAxisSize.min, children: [
            const _FaixaAtendimento(),
            _barra(c),
          ]),
        ),
      ),
    );
  }

  Widget _barra(AfCores c) {
    return Consumer<ServerModel>(builder: (context, sm, _) {
      final alerta = afAtendimentos(sm).isNotEmpty || (sm.isStart && (!sm.mediaOk || !sm.inputOk));
      Widget item(int i, IconData icone, String nome, {bool marca = false}) {
        final on = _aba == i;
        return Expanded(
          child: InkWell(
            onTap: () => setState(() => _aba = i),
            child: Padding(
              padding: const EdgeInsets.fromLTRB(0, 4, 0, 6),
              child: Column(mainAxisSize: MainAxisSize.min, children: [
                AnimatedContainer(
                  duration: const Duration(milliseconds: 200),
                  width: 60,
                  height: 30,
                  decoration: BoxDecoration(color: on ? c.primary.withOpacity(0.2) : Colors.transparent, borderRadius: BorderRadius.circular(15)),
                  child: Stack(alignment: Alignment.center, children: [
                    Icon(icone, size: 21, color: on ? c.accentText : c.muted),
                    if (marca)
                      Positioned(
                        top: 3,
                        right: 14,
                        child: Container(
                          width: 8,
                          height: 8,
                          decoration: BoxDecoration(color: c.cyan, shape: BoxShape.circle, border: Border.all(color: c.side, width: 2)),
                        ),
                      ),
                  ]),
                ),
                const SizedBox(height: 4),
                Text(nome, style: TextStyle(fontFamily: kFonte, fontSize: 11.5, fontWeight: FontWeight.w600, color: on ? c.text : c.muted)),
              ]),
            ),
          ),
        );
      }

      return Container(
        decoration: BoxDecoration(color: c.side, border: Border(top: BorderSide(color: c.line))),
        padding: const EdgeInsets.fromLTRB(8, 6, 8, 2),
        child: SafeArea(
          top: false,
          child: Row(children: [
            item(0, Icons.desktop_windows_outlined, 'Acessar'),
            item(1, Icons.phone_android_rounded, 'Este celular', marca: alerta),
            item(2, Icons.settings_outlined, 'Ajustes'),
          ]),
        ),
      );
    });
  }
}

/// Faixa fixa acima da barra enquanto alguém acessa este celular: quem, há quanto tempo,
/// conversa e encerrar (.liveband do protótipo). Fica visível em qualquer aba.
class _FaixaAtendimento extends StatefulWidget {
  const _FaixaAtendimento();

  @override
  State<_FaixaAtendimento> createState() => _FaixaAtendimentoState();
}

class _FaixaAtendimentoState extends State<_FaixaAtendimento> {
  // O Client do RustDesk não diz quando a conexão começou: anotamos quando a vimos.
  static final Map<int, DateTime> _inicio = {};
  Timer? _relogio;

  @override
  void initState() {
    super.initState();
    _relogio = Timer.periodic(const Duration(seconds: 1), (_) {
      if (mounted && _inicio.isNotEmpty) setState(() {});
    });
  }

  @override
  void dispose() {
    _relogio?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Consumer<ServerModel>(builder: (context, sm, _) {
      final ativos = afAtendimentos(sm);
      _inicio.removeWhere((id, _) => !ativos.any((a) => a.id == id));
      if (ativos.isEmpty) return const SizedBox.shrink();
      final cl = ativos.first;
      final t0 = _inicio.putIfAbsent(cl.id, DateTime.now);
      final c = AfCores.of(context);
      final quem = cl.name.isNotEmpty ? cl.name : 'ID ${cl.peerId}';
      final extra = ativos.length > 1 ? ' e mais ${ativos.length - 1}' : '';
      return Container(
        margin: const EdgeInsets.fromLTRB(12, 0, 12, 6),
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          color: Color.alphaBlend(c.cyan.withOpacity(0.14), c.surface),
          borderRadius: BorderRadius.circular(16),
          border: Border.all(color: c.cyan.withOpacity(0.4)),
        ),
        child: Row(children: [
          const AfSelo('', tom: AfTom.live, pulsando: true),
          const SizedBox(width: 10),
          Expanded(
            child: Column(crossAxisAlignment: CrossAxisAlignment.start, mainAxisSize: MainAxisSize.min, children: [
              Text('$quem$extra está acessando',
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(fontFamily: kFonte, fontSize: 13.5, fontWeight: FontWeight.w700, color: c.text)),
              Text('Este celular · ${_duracao(DateTime.now().difference(t0))}',
                  style: TextStyle(fontFamily: kFonte, fontSize: 12.5, color: c.muted, fontFeatures: const [FontFeature.tabularFigures()])),
            ]),
          ),
          const SizedBox(width: 8),
          AfBotaoIcone(Icons.chat_bubble_outline_rounded, dica: 'Conversa', onTap: () => _abrirConversa(context, cl)),
          const SizedBox(width: 8),
          AfBotao('Encerrar', pequeno: true, estilo: AfEstiloBotao.perigo, onTap: () => _encerrar(ativos)),
        ]),
      );
    });
  }

  static String _duracao(Duration d) {
    final m = d.inMinutes.toString().padLeft(2, '0');
    final s = (d.inSeconds % 60).toString().padLeft(2, '0');
    return d.inHours > 0 ? '${d.inHours}:$m:$s' : '$m:$s';
  }

  void _encerrar(List<Client> ativos) {
    // Mesmas chamadas do botão "Desconectar" da ServerPage antiga.
    for (final cl in ativos) {
      bind.cmCloseConnection(connId: cl.id);
      gFFI.invokeMethod('cancel_notification', cl.id);
    }
  }

  void _abrirConversa(BuildContext context, Client cl) {
    gFFI.chatModel.changeCurrentKey(MessageKey(cl.peerId, cl.id));
    Navigator.of(context).push(MaterialPageRoute(builder: (ctx) {
      final c = AfCores.of(ctx);
      return Scaffold(
        backgroundColor: c.bg,
        appBar: AppBar(
          backgroundColor: c.side,
          foregroundColor: c.text,
          elevation: 0,
          title: Text('Conversa', style: TextStyle(fontFamily: kFonte, fontSize: 17, fontWeight: FontWeight.w800, color: c.text)),
        ),
        body: ChatPage(type: ChatPageType.mobileMain),
      );
    }));
  }
}
