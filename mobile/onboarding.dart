// AcessoFast — assistente de primeira abertura (Android).
//
// Copiado pelo CI para flutter/lib/acessofast/onboarding.dart.
//
// ---------------------------------------------------------------------------
// POR QUE ISTO EXISTE
// ---------------------------------------------------------------------------
// Nenhuma das permissões que o AcessoFast precisa pode ser concedida
// automaticamente — captura de tela e acessibilidade são bloqueadas por design
// pelo Android, justamente por serem as que malware quer. Não existe API para
// pular, com ou sem app assinado.
//
// O que dá para fazer é tirar do cliente o trabalho de DESCOBRIR o que fazer.
// Sem isto, ele abre o app, não entende os toggles, e liga para o suporte.
//
// ---------------------------------------------------------------------------
// DECISÕES DE IMPLEMENTAÇÃO (para quem for mexer)
// ---------------------------------------------------------------------------
// - Reusa a infra do próprio RustDesk (dialogManager + CustomAlertDialog) em
//   vez de criar rota/tela nova. Menos superfície nova = menos chance de
//   quebrar num upgrade de tag do rustdesk/rustdesk.
// - Reusa gFFI.serverModel para AGIR e para LER estado. Nada de reimplementar
//   lógica de permissão: toggleService/toggleInput já fazem o certo, inclusive
//   os avisos que o RustDesk mostra antes.
// - Otimização de bateria entra como TEXTO, não botão: abrir aquela tela exige
//   Intent nativo, e adicionar Kotlin aqui aumentaria o risco do build sem
//   ganho proporcional. Fica para uma próxima, se doer na prática.
// - O consentimento da Acessibilidade (acessofastConsentimentoControle) mora
//   aqui, mas NÃO é chamado só pelo assistente: o CI troca o corpo do
//   showInputWarnAlert do RustDesk por ele, então vale para todo caminho que
//   liga o controle. É exigência da Play — ver o comentário da função.
// - O canal de instalação vem do build (--dart-define=ACESSOFAST_CANAL): o
//   .aab da Play sai com 'play', o APK direto com 'apk'. Não dá para ler o
//   instalador em Dart sem Kotlin novo, e o build já sabe a resposta.

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_hbb/common.dart';
import 'package:flutter_hbb/consts.dart';
import 'package:flutter_hbb/models/model.dart';
import 'package:path_provider/path_provider.dart';

const String _onboardingFlagFile = 'acessofast_onboarding.done';

/// true no .aab da Play Store. Instalado pela loja, o Android não aplica as
/// "configurações restritas" (é o sideload que as ativa), então o passo de
/// liberar some do assistente.
const bool _viaPlayStore =
    String.fromEnvironment('ACESSOFAST_CANAL') == 'play';

void _log(String msg) {
  // ignore: avoid_print
  print('[acessofast/onboarding] $msg');
}

Future<File> _flagFile() async {
  final dir = await getApplicationSupportDirectory();
  return File('${dir.path}/$_onboardingFlagFile');
}

Future<bool> _alreadyShown() async {
  try {
    return await (await _flagFile()).exists();
  } catch (_) {
    // Na dúvida NÃO mostra: repetir o assistente todo boot seria pior do que
    // não mostrar uma vez.
    return true;
  }
}

Future<void> _markShown() async {
  try {
    await (await _flagFile())
        .writeAsString(jsonEncode({'done': true}), flush: true);
  } catch (e) {
    _log('WARN não gravou a marca de concluído: $e');
  }
}

Widget _statusIcon(bool ok) => Icon(
      ok ? Icons.check_circle : Icons.radio_button_unchecked,
      color: ok ? Colors.green : Colors.grey,
      size: 22,
    );

Widget _step({
  required int n,
  required String titulo,
  required String descricao,
  required bool ok,
  required VoidCallback? onTap,
  String acao = 'Ativar',
}) {
  return Padding(
    padding: const EdgeInsets.symmetric(vertical: 6),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(padding: const EdgeInsets.only(top: 2), child: _statusIcon(ok)),
        const SizedBox(width: 10),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('$n. $titulo',
                  style: const TextStyle(fontWeight: FontWeight.w600)),
              const SizedBox(height: 2),
              Text(descricao, style: const TextStyle(fontSize: 12.5)),
            ],
          ),
        ),
        const SizedBox(width: 8),
        if (!ok && onTap != null)
          TextButton(onPressed: onTap, child: Text(acao))
        else
          const SizedBox(width: 8),
      ],
    ),
  );
}

Widget _itemConsentimento(String texto) => Padding(
      padding: const EdgeInsets.only(top: 6),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text('•  ', style: TextStyle(fontSize: 12.5)),
          Expanded(child: Text(texto, style: const TextStyle(fontSize: 12.5))),
        ],
      ),
    );

/// Divulgação + consentimento antes de abrir a Acessibilidade.
///
/// A Play exige isto de app que usa o AccessibilityService sem ser ferramenta
/// de acessibilidade: a tela tem que estar no próprio app, no fluxo normal,
/// dizer o que é acessado e para quê, e pedir aceite explícito (tocar em
/// "Concordo"). A declaração no Play Console pede um vídeo deste diálogo.
///
/// Substitui o showInputWarnAlert do RustDesk (patch no CI), que é o único
/// portão do toggleInput. "Agora não" fecha sem abrir nada.
///
/// O texto só afirma o que o app de fato faz: o serviço executa os toques e o
/// texto que o técnico conectado envia. Mexer nele exige reler o InputService
/// do RustDesk e regravar o vídeo da declaração.
void acessofastConsentimentoControle(FFI ffi) {
  ffi.dialogManager.show((setState, close, context) {
    void concordo() {
      try {
        AndroidPermissionManager.startAction(kActionAccessibilitySettings);
      } catch (e) {
        _log('falha ao abrir acessibilidade: $e');
      }
      close();
    }

    return CustomAlertDialog(
      title: const Text('Permitir o controle pelo técnico?'),
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text(
              'O AcessoFast usa o serviço de Acessibilidade do Android para que '
              'o técnico que está te atendendo possa tocar, deslizar e digitar '
              'na tela do seu celular por você.',
              style: TextStyle(fontSize: 13),
            ),
            const SizedBox(height: 10),
            const Text(
              'Como esse acesso é usado:',
              style: TextStyle(fontSize: 13, fontWeight: FontWeight.w600),
            ),
            _itemConsentimento(
                'Só age durante um atendimento, quando um técnico autorizado '
                'está conectado com a senha deste aparelho.'),
            _itemConsentimento(
                'Serve apenas para executar na tela os toques e o texto que o '
                'técnico enviar. O AcessoFast não usa esse acesso para coletar, '
                'guardar ou enviar dados seus.'),
            _itemConsentimento(
                'Você pode desligar quando quiser, em Configurações > '
                'Acessibilidade > AcessoFast Input.'),
            const SizedBox(height: 10),
            const Text(
              'Ao tocar em "Concordo", abriremos as configurações de '
              'Acessibilidade para você ativar "AcessoFast Input".',
              style: TextStyle(fontSize: 12.5),
            ),
            if (!_viaPlayStore) ...[
              const SizedBox(height: 8),
              const Text(
                'Se a opção aparecer acinzentada, antes libere em Informações '
                'do aplicativo > ⋮ > "Permitir configurações restritas".',
                style: TextStyle(fontSize: 12),
              ),
            ],
          ],
        ),
      ),
      actions: [
        TextButton(onPressed: close, child: const Text('Agora não')),
        TextButton(onPressed: concordo, child: const Text('Concordo')),
      ],
      onSubmit: concordo,
      onCancel: close,
    );
  });
}

/// Mostra o assistente. `forcar: true` ignora a marca de já exibido — usado
/// quando o técnico quiser reabrir o passo a passo no aparelho do cliente.
Future<void> showAcessofastOnboarding({bool forcar = false}) async {
  try {
    if (!forcar && await _alreadyShown()) return;

    final sm = gFFI.serverModel;

    await gFFI.dialogManager.show<void>((setState, close, context) {
      // Reflete no diálogo o que o usuário fez nas telas do Android. O
      // serverModel só reavalia quando o app volta ao foco, então um refresh
      // periódico é o jeito simples de manter os ✅ corretos sem depender de
      // ciclo de vida.
      Timer.periodic(const Duration(seconds: 2), (t) {
        try {
          setState(() {});
        } catch (_) {
          t.cancel();
        }
      });

      final tudoPronto = sm.isStart && sm.mediaOk && sm.inputOk;

      return CustomAlertDialog(
        title: Row(children: const [
          Icon(Icons.verified_user_outlined, size: 24),
          SizedBox(width: 10),
          Text('Preparar o AcessoFast'),
        ]),
        content: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text(
                'Para o suporte conseguir te atender, o Android exige que você '
                'libere alguns acessos. É rápido e só precisa ser feito uma vez.',
                style: TextStyle(fontSize: 13),
              ),
              const SizedBox(height: 14),
              _step(
                n: 1,
                titulo: 'Notificações',
                descricao:
                    'Mostra um aviso enquanto o atendimento estiver acontecendo, '
                    'para você sempre saber que alguém está conectado.',
                ok: true, // pedida direto; sem estado confiável para ler
                onTap: null,
              ),
              _step(
                n: 2,
                titulo: 'Compartilhar a tela',
                descricao:
                    'Permite que o técnico veja a tela do seu celular. O Android '
                    'vai pedir uma confirmação — toque em "Iniciar agora".',
                ok: sm.isStart && sm.mediaOk,
                onTap: () async {
                  try {
                    await sm.checkRequestNotificationPermission();
                    await sm.toggleService();
                  } catch (e) {
                    _log('falha ao iniciar o serviço: $e');
                  }
                  setState(() {});
                },
              ),
              // PASSO 3 e 4 juntos resolvem o "controle". Foram SEPARADOS de
              // propósito: em aparelho com o app instalado fora da Play Store
              // (todo cliente hoje), o Android TRANCA a chave de Acessibilidade
              // até o usuário "Permitir configurações restritas" na tela de
              // Informações do aplicativo. Levar direto pra Acessibilidade (o
              // que o passo único fazia) caía numa opção acinzentada — a
              // reclamação nº1. Não existe API pra ler nem pra pular esse
              // estado, então guiamos os dois toques, na ordem certa.
              //
              // Os dois passos só ficam verdes quando o input REALMENTE liga
              // (sm.inputOk) — que é a única prova confiável de que a restrição
              // foi liberada. Assim o botão "Liberar" continua disponível caso
              // o cliente erre o caminho e precise voltar. Instalado pela Play
              // Store o passo 3 não existe (sideload é o que ativa a restrição),
              // então ele some e o "Ativar" vira o passo 3.
              if (!_viaPlayStore)
                _step(
                  n: 3,
                  titulo: 'Liberar o controle',
                  descricao:
                      'Como o app foi instalado fora da Play Store, o Android '
                      'tranca o próximo passo até você liberar. Vai abrir '
                      '"Informações do aplicativo" — toque em "Permitir '
                      'configurações restritas" (no topo; em alguns aparelhos, no '
                      'menu ⋮ do canto).',
                  ok: sm.inputOk,
                  acao: 'Liberar',
                  onTap: () async {
                    try {
                      AndroidPermissionManager.startAction(
                          'android.settings.APPLICATION_DETAILS_SETTINGS');
                    } catch (e) {
                      _log('falha ao abrir Informações do aplicativo: $e');
                    }
                    setState(() {});
                  },
                ),
              _step(
                n: _viaPlayStore ? 3 : 4,
                titulo: 'Ativar o controle',
                descricao: _viaPlayStore
                    ? 'Deixa o técnico tocar na tela por você, em vez de só '
                        'olhar. Abre a Acessibilidade — ative "AcessoFast Input".'
                    : 'Deixa o técnico tocar na tela por você, em vez de só '
                        'olhar. Abre a Acessibilidade — ative "AcessoFast Input". '
                        'Se a opção estiver acinzentada, volte ao passo 3.',
                ok: sm.inputOk,
                onTap: () async {
                  try {
                    await sm.toggleInput();
                  } catch (e) {
                    _log('falha ao abrir acessibilidade: $e');
                  }
                  setState(() {});
                },
              ),
              const SizedBox(height: 10),
              Container(
                padding: const EdgeInsets.all(10),
                decoration: BoxDecoration(
                  color: Colors.orange.withOpacity(0.12),
                  borderRadius: BorderRadius.circular(6),
                ),
                child: const Text(
                  'Dica: em Configurações > Bateria, marque o AcessoFast como '
                  '"Sem restrição". Sem isso o Android pode desligar o app '
                  'sozinho e o suporte não conseguirá te encontrar.',
                  style: TextStyle(fontSize: 12),
                ),
              ),
              if (tudoPronto) ...[
                const SizedBox(height: 12),
                Row(children: const [
                  Icon(Icons.check_circle, color: Colors.green, size: 20),
                  SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      'Tudo pronto. Já pode fechar.',
                      style: TextStyle(fontWeight: FontWeight.w600),
                    ),
                  ),
                ]),
              ],
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () {
              // Marca como visto mesmo se pulou: insistir a cada abertura
              // irrita. O técnico consegue reabrir com forcar: true.
              _markShown();
              close();
            },
            child: Text(tudoPronto ? 'Concluir' : 'Agora não'),
          ),
        ],
      );
    });
  } catch (e, s) {
    // NUNCA derruba o app por causa do assistente.
    _log('assistente falhou: $e\n$s');
  }
}
