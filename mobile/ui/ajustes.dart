// Aba "Ajustes" (etapa 8): conta, aparência, este celular e sobre. Desenho do protótipo aprovado
// (prototipo-mobile, telaConfig). Copiado pelo CI para flutter/lib/acessofast/ui/.
//
// As chaves são as mesmas da SettingsPage do RustDesk (tema, iniciar com o celular, tela ligada).
// O resto das opções dele continua em "Configurações avançadas", que abre a tela antiga: nada
// some do app.
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_hbb/common.dart';
import 'package:flutter_hbb/consts.dart';
import 'package:flutter_hbb/mobile/pages/settings_page.dart';
import 'package:flutter_hbb/models/platform_model.dart';
import 'package:url_launcher/url_launcher.dart';

import '../onboarding.dart';
import 'comum.dart';
import 'painel.dart';
import 'registro.dart';
import 'tema.dart';

class AfAjustes extends StatefulWidget {
  const AfAjustes({super.key});

  @override
  State<AfAjustes> createState() => _AfAjustesState();
}

class _AfAjustesState extends State<AfAjustes> with WidgetsBindingObserver {
  bool _iniciarComCelular = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    unawaited(_lerIniciar());
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) unawaited(_lerIniciar());
  }

  Future<void> _lerIniciar() async {
    try {
      final v = await gFFI.invokeMethod(AndroidChannel.kGetStartOnBootOpt);
      if (mounted) setState(() => _iniciarComCelular = v == true);
    } catch (e) {
      debugPrint('ajustes: $e');
    }
  }

  /// Mesmo caminho da SettingsPage: ligar exige "sem restrição de bateria" e "sobrepor a outros
  /// apps"; sem elas o Android não deixa o serviço subir sozinho.
  Future<void> _alternarIniciar(bool ligar) async {
    if (ligar) {
      if (!await AndroidPermissionManager.check(kRequestIgnoreBatteryOptimizations) &&
          !await AndroidPermissionManager.request(kRequestIgnoreBatteryOptimizations)) {
        return;
      }
      if (!await AndroidPermissionManager.check(kSystemAlertWindow) && !await AndroidPermissionManager.request(kSystemAlertWindow)) {
        return;
      }
    }
    await gFFI.invokeMethod(AndroidChannel.kSetStartOnBootOpt, ligar);
    if (mounted) setState(() => _iniciarComCelular = ligar);
  }

  bool get _telaLigada => bind.mainGetLocalOption(key: kOptionKeepScreenOn) != 'never';

  Future<void> _alternarTela(bool ligar) async {
    await bind.mainSetLocalOption(key: kOptionKeepScreenOn, value: ligar ? 'during-controlled' : 'never');
    gFFI.serverModel.androidUpdatekeepScreenOn();
    if (mounted) setState(() {});
  }

  Future<void> _tema(ThemeMode m) async {
    await MyTheme.changeDarkMode(m);
    if (mounted) setState(() {});
  }

  void _abrir(String url) => unawaited(launchUrl(Uri.parse(url), mode: LaunchMode.externalApplication));

  @override
  Widget build(BuildContext context) {
    final c = AfCores.of(context);
    final painel = Painel.instancia;
    return AnimatedBuilder(
      animation: painel,
      builder: (context, _) {
        final p = painel.perfil;
        final k = painel.carteira;
        final tema = MyTheme.getThemeModePreference();
        return ListView(padding: const EdgeInsets.fromLTRB(18, 8, 18, 24), children: [
          const AfTitulo('Ajustes'),
          const SizedBox(height: 14),
          const AfSecao('Conta'),
          const SizedBox(height: 8),
          if (p == null)
            const AfLista([
              AfLinha(
                icone: Icons.person_outline_rounded,
                titulo: 'Sem conta neste celular',
                sub: 'Para acessar os computadores da empresa, entre pela aba Acessar',
              ),
            ])
          else
            AfLista([
              AfLinha(icone: Icons.person_outline_rounded, titulo: p.nome, sub: p.email),
              if (painel.empresas.isNotEmpty)
                AfLinha(
                  icone: Icons.apartment_outlined,
                  titulo: painel.empresas.length == 1 ? painel.empresas.values.first : '${painel.empresas.length} empresas',
                  sub: k == null
                      ? null
                      : k.modo == 'plan'
                          ? 'Plano ativo'
                          : '${k.modo == 'free' ? 'Plano gratuito' : 'Por créditos'} · ${k.creditos} ${k.creditos == 1 ? 'crédito' : 'créditos'}',
                ),
              AfLinha(
                icone: Icons.logout_rounded,
                titulo: 'Sair da conta',
                sub: 'Este celular continua podendo ser acessado',
                corTitulo: c.dangerText,
                onTap: () async {
                  await painel.sair();
                  showToast('Você saiu da conta.');
                },
              ),
            ]),
          const SizedBox(height: 16),
          const AfSecao('Aparência'),
          const SizedBox(height: 8),
          Container(
            padding: const EdgeInsets.all(3),
            decoration: BoxDecoration(color: c.surface, borderRadius: BorderRadius.circular(12), border: Border.all(color: c.line)),
            child: Row(children: [
              for (final (m, t) in [(ThemeMode.system, 'Igual ao celular'), (ThemeMode.light, 'Claro'), (ThemeMode.dark, 'Escuro')])
                Expanded(
                  child: GestureDetector(
                    onTap: () => _tema(m),
                    child: Container(
                      height: 34,
                      alignment: Alignment.center,
                      decoration: BoxDecoration(
                        color: tema == m ? c.surface2 : Colors.transparent,
                        borderRadius: BorderRadius.circular(9),
                        border: Border.all(color: tema == m ? c.line2 : Colors.transparent),
                      ),
                      child: Text(t, style: TextStyle(fontFamily: kFonte, fontSize: 13, fontWeight: FontWeight.w700, color: tema == m ? c.text : c.muted)),
                    ),
                  ),
                ),
            ]),
          ),
          const SizedBox(height: 16),
          const AfSecao('Este celular'),
          const SizedBox(height: 8),
          AfLista([
            AfLinha(
              titulo: 'Iniciar junto com o celular',
              sub: 'Fica pronto depois de reiniciar',
              fim: Switch(value: _iniciarComCelular, onChanged: _alternarIniciar),
            ),
            AfLinha(
              titulo: 'Manter a tela ligada no acesso',
              sub: 'A tela não apaga enquanto o técnico trabalha',
              fim: Switch(value: _telaLigada, onChanged: _alternarTela),
            ),
            AfLinha(
              titulo: 'Revisar permissões',
              sub: 'Abre de novo o passo a passo',
              fim: Icon(Icons.chevron_right_rounded, color: c.muted),
              onTap: () => showAcessofastOnboarding(forcar: true),
            ),
            AfLinha(
              titulo: 'Configurações avançadas',
              sub: 'Todas as opções, na tela completa',
              fim: Icon(Icons.chevron_right_rounded, color: c.muted),
              onTap: () => Navigator.of(context).push(MaterialPageRoute(
                builder: (ctx) => Scaffold(
                  backgroundColor: AfCores.of(ctx).bg,
                  appBar: AppBar(
                    backgroundColor: AfCores.of(ctx).side,
                    foregroundColor: AfCores.of(ctx).text,
                    elevation: 0,
                    title: Text('Configurações avançadas',
                        style: TextStyle(fontFamily: kFonte, fontSize: 17, fontWeight: FontWeight.w800, color: AfCores.of(ctx).text)),
                  ),
                  body: SettingsPage(),
                ),
              )),
            ),
          ]),
          const SizedBox(height: 16),
          const AfSecao('Sobre'),
          const SizedBox(height: 8),
          AfLista([
            AfLinha(
              lider: ClipRRect(
                borderRadius: BorderRadius.circular(8),
                child: Image.asset('assets/acessofast/icon.png', width: 32, height: 32, errorBuilder: (_, __, ___) => const SizedBox(width: 32)),
              ),
              titulo: 'AcessoFast',
              sub: textoVersao(),
            ),
            AfLinha(
              titulo: 'Política de privacidade',
              fim: Icon(Icons.open_in_new_rounded, size: 18, color: c.muted),
              onTap: () => _abrir('https://acessofast.com.br/privacidade'),
            ),
            AfLinha(
              titulo: 'Excluir meus dados',
              fim: Icon(Icons.open_in_new_rounded, size: 18, color: c.muted),
              onTap: () => _abrir('https://acessofast.com.br/exclusao-de-dados'),
            ),
            AfLinha(
              titulo: 'Falar com o suporte',
              sub: 'suporte@acessofast.com.br',
              fim: Icon(Icons.open_in_new_rounded, size: 18, color: c.muted),
              onTap: () => _abrir('mailto:suporte@acessofast.com.br'),
            ),
          ]),
        ]);
      },
    );
  }
}
