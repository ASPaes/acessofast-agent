// Pedido de acesso no celular do cliente (etapa 3): quem quer entrar, o que vai poder fazer, e
// Recusar / Permitir. Desenho do protótipo aprovado (prototipo-mobile, overlay "pedido").
// Copiado pelo CI para flutter/lib/acessofast/ui/; o CI troca o corpo do showLoginDialog do
// ServerModel por afPedidoDeAcesso.
//
// Só aparece para quem NÃO vem pelo painel: o técnico da empresa entra com a senha automática e
// não pede nada. As respostas são as do RustDesk (sendLoginResponse), com a mesma etiqueta de
// diálogo, para o RustDesk fechar sozinho quando a conexão cair ou for aceita por outro caminho.
import 'package:flutter/material.dart';
import 'package:flutter_hbb/common.dart' show CustomAlertDialog;
import 'package:flutter_hbb/models/server_model.dart';

import 'comum.dart';
import 'tema.dart';

void afPedidoDeAcesso(ServerModel sm, Client client) {
  sm.parent.target?.dialogManager.show((setState, close, context) {
    void responder(bool ok) {
      sm.sendLoginResponse(client, ok);
      close();
    }

    // O dialogManager só aceita CustomAlertDialog; o desenho dele vem do nosso tema (tema.dart).
    return CustomAlertDialog(content: _Pedido(client: client, podeAceitar: sm.approveMode != 'password', responder: responder));
  }, tag: getLoginDialogTag(client.id));
}

class _Pedido extends StatelessWidget {
  final Client client;
  final bool podeAceitar;
  final void Function(bool) responder;
  const _Pedido({required this.client, required this.podeAceitar, required this.responder});

  @override
  Widget build(BuildContext context) {
    final c = AfCores.of(context);
    final nome = client.name.trim().isNotEmpty ? client.name.trim() : 'Alguém';
    final iniciais = nome.split(RegExp(r'\s+')).where((p) => p.isNotEmpty).take(2).map((p) => p[0].toUpperCase()).join();
    final oQue = client.isFileTransfer
        ? 'Quer enviar e receber arquivos deste celular.'
        : client.isViewCamera
            ? 'Quer ver a câmera deste celular.'
            : client.isTerminal
                ? 'Quer abrir um terminal neste celular.'
                : 'Quer acessar este celular.';
    Widget pode(IconData icone, String t, bool sim) => Padding(
          padding: const EdgeInsets.symmetric(vertical: 4),
          child: Row(children: [
            Icon(sim ? icone : Icons.block_rounded, size: 18, color: sim ? c.ok : c.muted),
            const SizedBox(width: 10),
            Expanded(child: Text(t, style: TextStyle(fontFamily: kFonte, fontSize: 13.5, color: sim ? c.text : c.muted))),
          ]),
        );
    return Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start, children: [
              Row(children: [
                Container(
                  width: 48,
                  height: 48,
                  alignment: Alignment.center,
                  decoration: BoxDecoration(shape: BoxShape.circle, gradient: LinearGradient(colors: [c.primary, const Color(0xFF6B8CFF)])),
                  child: Text(iniciais, style: const TextStyle(fontFamily: kFonte, fontSize: 16, fontWeight: FontWeight.w800, color: Colors.white)),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                    const AfRotulo('Pedido de acesso'),
                    const SizedBox(height: 2),
                    Text(nome,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(fontFamily: kFonte, fontSize: 19, fontWeight: FontWeight.w800, color: c.text)),
                    Text('ID ${client.peerId}', style: TextStyle(fontFamily: kFonteMono, fontSize: 12, color: c.muted)),
                  ]),
                ),
              ]),
              const SizedBox(height: 14),
              Text('$oQue Só permita se você estiver esperando esse atendimento.',
                  style: TextStyle(fontFamily: kFonte, fontSize: 13.5, height: 1.45, color: c.text2)),
              const SizedBox(height: 12),
              AfCartao(
                padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
                child: Column(children: [
                  if (!client.isFileTransfer) pode(Icons.touch_app_outlined, 'Tocar na tela', client.keyboard),
                  pode(Icons.folder_outlined, 'Enviar e receber arquivos', client.file),
                  if (!client.isFileTransfer) pode(Icons.volume_up_outlined, 'Ouvir o som do celular', client.audio),
                ]),
              ),
              const SizedBox(height: 6),
              const AfMiudo('Dá para mudar isso em Este celular › Permissões.'),
              const SizedBox(height: 16),
              Row(children: [
                Expanded(child: AfBotao('Recusar', estilo: AfEstiloBotao.perigo, onTap: () => responder(false))),
                if (podeAceitar) ...[
                  const SizedBox(width: 8),
                  Expanded(child: AfBotao('Permitir', estilo: AfEstiloBotao.primario, onTap: () => responder(true))),
                ],
              ]),
            ]);
  }
}
