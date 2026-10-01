// Aba "Este celular": o aparelho sendo acessado. Desenho do protótipo aprovado
// (prototipo-mobile, telaCelular). Copiado pelo CI para flutter/lib/acessofast/ui/.
//
// Só troca o desenho. Ligar, desligar, permissões e conexões continuam sendo do
// gFFI.serverModel do RustDesk, exatamente as mesmas chamadas da ServerPage antiga.
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_hbb/common.dart';
import 'package:flutter_hbb/models/server_model.dart';
import 'package:provider/provider.dart';

import '../onboarding.dart';
import 'comum.dart';
import 'tema.dart';

/// Conexões que estão de fato acontecendo (aceitas e ainda abertas).
List<Client> afAtendimentos(ServerModel sm) => sm.clients.where((c) => c.authorized && !c.disconnected).toList();

class AfEsteCelular extends StatefulWidget {
  const AfEsteCelular({super.key});

  @override
  State<AfEsteCelular> createState() => _AfEsteCelularState();
}

class _AfEsteCelularState extends State<AfEsteCelular> with WidgetsBindingObserver {
  Timer? _timer;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    // O mesmo que a ServerPage fazia ao abrir: buscar o ID e ler as permissões.
    _timer = Timer.periodic(const Duration(seconds: 3), (_) => gFFI.serverModel.fetchID());
    gFFI.serverModel.fetchID();
    gFFI.serverModel.checkAndroidPermission();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // Voltou das telas do Android (Acessibilidade, captura): relê as permissões.
    if (state == AppLifecycleState.resumed) gFFI.serverModel.checkAndroidPermission();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _timer?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return ChangeNotifierProvider.value(
      value: gFFI.serverModel,
      child: Consumer<ServerModel>(builder: (context, sm, _) => _conteudo(context, sm)),
    );
  }

  Widget _conteudo(BuildContext context, ServerModel sm) {
    final c = AfCores.of(context);
    final emAtendimento = afAtendimentos(sm).isNotEmpty;
    final faltaTela = !sm.mediaOk;
    final faltaControle = !sm.inputOk;
    final faltam = (faltaTela ? 1 : 0) + (faltaControle ? 1 : 0);
    final id = sm.serverId.text;

    final AfSelo selo;
    if (emAtendimento) {
      selo = const AfSelo('Sendo acessado', tom: AfTom.live, pulsando: true);
    } else if (!sm.isStart) {
      selo = const AfSelo('Desligado', tom: AfTom.off);
    } else if (faltam > 0) {
      selo = const AfSelo('Falta permissão', tom: AfTom.warn);
    } else if (sm.connectStatus != 1) {
      selo = const AfSelo('Conectando ao servidor', tom: AfTom.warn, pulsando: true);
    } else {
      selo = const AfSelo('Pronto para receber acesso', tom: AfTom.ok);
    }

    return ListView(
      padding: const EdgeInsets.fromLTRB(18, 8, 18, 24),
      children: [
        const AfTitulo('Este celular'),
        const SizedBox(height: 14),
        AfCartao(
          destaque: true,
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Row(children: [
              const Expanded(child: AfRotulo('Receber acesso')),
              Switch(value: sm.isStart, onChanged: (_) => sm.toggleService()),
            ]),
            const SizedBox(height: 4),
            selo,
            const SizedBox(height: 14),
            const AfRotulo('ID deste celular'),
            const SizedBox(height: 6),
            Row(children: [
              Expanded(child: AfIdGrande(id)),
              const SizedBox(width: 10),
              AfBotaoIcone(Icons.copy_rounded, dica: 'Copiar o ID', onTap: () => _copiar(sm.serverId.text.replaceAll(' ', ''))),
            ]),
            const SizedBox(height: 14),
            Divider(height: 1, color: c.line),
            const SizedBox(height: 12),
            // Não há senha para mostrar: o agente põe o aparelho em senha permanente,
            // sincronizada com o painel e trocada no fim de cada atendimento
            // (session.dart). Quem não vem pelo painel cai no "clique para aceitar".
            Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
              Icon(Icons.verified_user_outlined, size: 20, color: c.ok),
              const SizedBox(width: 10),
              Expanded(
                child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                  Text('Senha automática pelo painel',
                      style: TextStyle(fontFamily: kFonte, fontSize: 14, fontWeight: FontWeight.w700, color: c.text)),
                  const SizedBox(height: 2),
                  const AfMiudo('Os técnicos da sua empresa entram sem pedir senha. Qualquer outra pessoa só entra se você aprovar na hora.'),
                ]),
              ),
            ]),
          ]),
        ),
        if (sm.isStart && faltam > 0) ...[
          const SizedBox(height: 14),
          AfCartao(
            borda: c.warn.withOpacity(0.4),
            child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              Row(children: [
                Expanded(
                  child: Text(faltam == 1 ? 'Falta liberar 1 permissão' : 'Faltam $faltam permissões',
                      style: TextStyle(fontFamily: kFonte, fontSize: 14, fontWeight: FontWeight.w700, color: c.text)),
                ),
                Icon(Icons.info_outline_rounded, size: 20, color: c.warn),
              ]),
              const SizedBox(height: 6),
              AfMiudo(faltaTela ? 'Sem isso o técnico não vê a sua tela.' : 'Sem isso o técnico não consegue tocar na tela por você.'),
              const SizedBox(height: 12),
              AfBotao('Resolver agora', estilo: AfEstiloBotao.primario, largo: true, onTap: () => showAcessofastOnboarding(forcar: true)),
            ]),
          ),
        ],
        const SizedBox(height: 14),
        const AfSecao('Permissões'),
        const SizedBox(height: 8),
        AfLista([
          _permissao(sm, Icons.cast_rounded, 'Compartilhar a tela', 'O técnico pode ver a sua tela', 'Sem isso o técnico não vê nada', sm.mediaOk,
              sm.toggleService),
          _permissao(sm, Icons.touch_app_outlined, 'Controle pelo técnico', 'Ativo em Acessibilidade', 'O técnico só consegue olhar', sm.inputOk,
              sm.toggleInput),
          _permissao(sm, Icons.folder_outlined, 'Transferir arquivos', 'O técnico pode enviar e receber arquivos', 'Desligado', sm.fileOk, sm.toggleFile,
              opcional: true),
          if (androidVersion >= 30)
            _permissao(sm, Icons.volume_up_outlined, 'Som do celular', 'O técnico ouve o som do celular', 'Desligado', sm.audioOk, sm.toggleAudio,
                opcional: true),
        ]),
        const SizedBox(height: 14),
        _dicaBateria(c),
      ],
    );
  }

  Widget _permissao(ServerModel sm, IconData icone, String titulo, String okTxt, String faltaTxt, bool ok, VoidCallback alternar,
      {bool opcional = false}) {
    // Obrigatórias: sem "desligar" aqui, como no protótipo; quem quiser desliga em
    // Receber acesso (tela) ou nas configurações do Android (Acessibilidade).
    if (!opcional) {
      return AfLinha(
        icone: ok ? Icons.check_rounded : icone,
        tomIcone: ok ? AfTomIcone.ok : AfTomIcone.warn,
        titulo: titulo,
        sub: ok ? okTxt : faltaTxt,
        fim: ok ? null : AfBotao('Ativar', pequeno: true, estilo: AfEstiloBotao.primario, onTap: alternar),
      );
    }
    return AfLinha(
      icone: icone,
      tomIcone: ok ? AfTomIcone.ok : AfTomIcone.normal,
      titulo: titulo,
      sub: ok ? okTxt : faltaTxt,
      fim: Switch(value: ok, onChanged: (_) => alternar()),
    );
  }

  Widget _dicaBateria(AfCores c) => AfCartao(
        child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Icon(Icons.battery_charging_full_rounded, size: 20, color: c.warn),
          const SizedBox(width: 12),
          const Expanded(
            child: AfMiudo('Dica: em Configurações › Bateria, deixe o AcessoFast como "Sem restrições". '
                'Alguns celulares fecham o app sozinhos e o acesso cai.'),
          ),
        ]),
      );

  void _copiar(String texto) {
    Clipboard.setData(ClipboardData(text: texto));
    showToast('ID copiado');
  }
}
