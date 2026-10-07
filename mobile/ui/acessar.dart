// Aba "Acessar": o técnico entra com a conta do painel e acessa os computadores da empresa
// (etapas 4 e 5). Desenho do protótipo aprovado (prototipo-mobile, telaAcessar).
// Copiado pelo CI para flutter/lib/acessofast/ui/.
//
// Regras do Ryan (01/10/2026): no celular, só conta com plano ou com créditos acessa; a conta
// gratuita sempre gasta crédito (os acessos grátis do dia valem só no computador); compra só no
// painel. A conexão é a mesma do desktop e do painel: edge connect-device com o JWT do técnico
// (painel.dart, cópia do desktop), que dá a senha, registra o técnico e cobra.
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_hbb/common.dart';
import 'package:flutter_hbb/models/platform_model.dart';

import 'comum.dart';
import 'painel.dart';
import 'tema.dart';

// Cores dos marcadores: as mesmas 8 do painel, no tom do protótipo (igual ao empresa.dart do desktop).
Color corDoMarcador(BuildContext context, String cor) {
  final escuro = Theme.of(context).brightness == Brightness.dark;
  const e = {
    'slate': Color(0xFF94A3B8), 'red': Color(0xFFF87171), 'amber': Color(0xFFF5B942), 'green': Color(0xFF4ADE80),
    'blue': Color(0xFF6B9BFF), 'violet': Color(0xFFC084FC), 'pink': Color(0xFFF472B6), 'gray': Color(0xFFA1A1AA),
  };
  const c = {
    'slate': Color(0xFF475569), 'red': Color(0xFFC42F2F), 'amber': Color(0xFFA8660B), 'green': Color(0xFF15803D),
    'blue': Color(0xFF1D54E0), 'violet': Color(0xFF7C3AED), 'pink': Color(0xFFBE185D), 'gray': Color(0xFF52525B),
  };
  return (escuro ? e : c)[cor] ?? (escuro ? e : c)['slate']!;
}

/// CNPJ (14 dígitos) ou CPF (11) com a pontuação de sempre; igual ao formatarDocumento do desktop.
String formatarDocumento(String? doc) {
  final d = (doc ?? '').replaceAll(RegExp(r'\D'), '');
  if (d.length == 14) return '${d.substring(0, 2)}.${d.substring(2, 5)}.${d.substring(5, 8)}/${d.substring(8, 12)}-${d.substring(12)}';
  if (d.length == 11) return '${d.substring(0, 3)}.${d.substring(3, 6)}.${d.substring(6, 9)}-${d.substring(9)}';
  return doc ?? '';
}

String formatarId(String id) {
  final d = id.replaceAll(' ', '');
  if (d.length != 9 && d.length != 10) return id;
  return [for (var i = 0; i < d.length; i += 3) d.substring(i, i + 3 > d.length ? d.length : i + 3)].join(' ');
}

String _nomeCliente(ComputadorEmpresa d) => (d.cliente ?? d.grupo ?? '').trim();

IconData iconeDoSistema(String os) {
  final s = os.toLowerCase();
  if (s.contains('mac') || s.contains('darwin')) return Icons.laptop_mac_rounded;
  if (s.contains('android')) return Icons.phone_android_rounded;
  if (s.contains('linux')) return Icons.terminal_rounded;
  return Icons.desktop_windows_outlined;
}

/// Conta do técnico pode acessar pelo celular? Plano, ou créditos > 0. Sem empresa (administrador
/// da plataforma) não há carteira e o acesso fica livre, como no painel.
bool podeAcessarPeloCelular(Painel p) {
  final k = p.carteira;
  if (p.perfil?.empresaId == null || k == null) return true;
  return k.modo == 'plan' || k.creditos > 0;
}

enum _Lista { todos, recentes, favoritos }

class AfAcessar extends StatefulWidget {
  const AfAcessar({super.key});

  @override
  State<AfAcessar> createState() => _AfAcessarState();
}

class _AfAcessarState extends State<AfAcessar> {
  static bool _restaurou = false;
  final _painel = Painel.instancia;
  final _busca = TextEditingController();
  final Set<String> _filtro = {};
  _Lista _lista = _Lista.todos;
  List<String> _favoritos = [];
  Set<String> _recentes = {};

  @override
  void initState() {
    super.initState();
    if (!_restaurou) {
      _restaurou = true;
      unawaited(_painel.restaurar());
    }
    unawaited(_carregarLocais());
    _busca.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _busca.dispose();
    super.dispose();
  }

  /// Recentes e favoritos ficam no próprio aparelho (RustDesk), pelo ID do computador.
  Future<void> _carregarLocais() async {
    try {
      await bind.mainLoadRecentPeers();
      final favs = await bind.mainGetFav();
      if (!mounted) return;
      setState(() {
        _favoritos = List<String>.from(favs);
        _recentes = gFFI.recentPeersModel.peers.map((p) => p.id).toSet();
      });
    } catch (e) {
      debugPrint('acessar: $e');
    }
  }

  Future<void> _alternarFavorito(ComputadorEmpresa d) async {
    final favs = List<String>.from(await bind.mainGetFav());
    final era = favs.contains(d.rustdeskId);
    era ? favs.remove(d.rustdeskId) : favs.add(d.rustdeskId);
    await bind.mainStoreFav(favs: favs);
    if (mounted) setState(() => _favoritos = favs);
    showToast(era ? 'Saiu dos favoritos.' : 'Adicionado aos favoritos.');
  }

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(animation: _painel, builder: (context, _) => _conteudo(context));
  }

  Widget _conteudo(BuildContext context) {
    final c = AfCores.of(context);
    final p = _painel.perfil;
    final topo = AfTitulo(
      'Acessar',
      sub: p == null ? null : (_painel.empresas.length == 1 ? _painel.empresas.values.first : null),
      direita: p == null ? null : _Avatar(p.nome),
    );

    if (p == null) {
      return ListView(padding: const EdgeInsets.fromLTRB(18, 8, 18, 24), children: [
        topo,
        const SizedBox(height: 14),
        AfCartao(
          destaque: true,
          padding: const EdgeInsets.fromLTRB(20, 28, 20, 22),
          child: Column(children: [
            _Arte(Icons.desktop_windows_outlined, c.accentText),
            const SizedBox(height: 14),
            Text('Acesse os computadores da sua empresa',
                textAlign: TextAlign.center, style: TextStyle(fontFamily: kFonte, fontSize: 20, fontWeight: FontWeight.w800, color: c.text)),
            const SizedBox(height: 8),
            const AfMiudo('Entre com a mesma conta do painel. Os computadores, os clientes e os marcadores aparecem aqui do jeito que estão lá.',
                alinhar: TextAlign.center),
            const SizedBox(height: 16),
            AfBotao(_painel.carregando ? 'Entrando…' : 'Entrar com a conta do painel',
                estilo: AfEstiloBotao.primario, largo: true, onTap: _painel.carregando ? null : () => _entrar(context)),
          ]),
        ),
        const SizedBox(height: 14),
        AfCartao(
          child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Icon(Icons.info_outline_rounded, size: 20, color: c.muted),
            const SizedBox(width: 12),
            const Expanded(
              child: AfMiudo('O acesso pelo celular é para contas com plano ou com créditos. Se você só quer receber suporte, não precisa '
                  'entrar: use a aba Este celular.'),
            ),
          ]),
        ),
      ]);
    }

    if (!podeAcessarPeloCelular(_painel)) {
      return RefreshIndicator(
        onRefresh: _painel.atualizarCarteira,
        child: ListView(padding: const EdgeInsets.fromLTRB(18, 8, 18, 24), children: [
          topo,
          const SizedBox(height: 14),
          AfCartao(
            destaque: true,
            padding: const EdgeInsets.fromLTRB(20, 28, 20, 22),
            child: Column(children: [
              _Arte(Icons.lock_outline_rounded, c.warn),
              const SizedBox(height: 14),
              Text('O acesso pelo celular é para contas com plano ou créditos',
                  textAlign: TextAlign.center, style: TextStyle(fontFamily: kFonte, fontSize: 20, fontWeight: FontWeight.w800, color: c.text)),
              const SizedBox(height: 8),
              const AfMiudo('Sua empresa está no plano gratuito e sem créditos. No computador, os acessos grátis do dia continuam valendo normalmente.',
                  alinhar: TextAlign.center),
            ]),
          ),
          const SizedBox(height: 14),
          AfCartao(
            child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              Text('Como liberar', style: TextStyle(fontFamily: kFonte, fontSize: 14, fontWeight: FontWeight.w700, color: c.text)),
              const SizedBox(height: 6),
              AfMiudo(p.admin
                  ? 'Contrate um plano ou compre créditos no painel, em Financeiro. Assim que o pagamento cair, o acesso libera aqui sozinho.'
                  : 'Peça ao administrador da sua empresa para contratar um plano ou comprar créditos no painel.'),
              const SizedBox(height: 12),
              AfBotao('Já liberei, verificar de novo', icone: Icons.refresh_rounded, largo: true, onTap: () async {
                await _painel.atualizarCarteira();
                if (!podeAcessarPeloCelular(_painel)) showToast('Ainda sem plano nem créditos.');
              }),
            ]),
          ),
        ]),
      );
    }

    return RefreshIndicator(
      onRefresh: () async {
        await Future.wait([_painel.atualizar().catchError((_) {}), _carregarLocais()]);
      },
      child: ListView(padding: const EdgeInsets.fromLTRB(18, 8, 18, 24), children: [
        topo,
        const SizedBox(height: 14),
        _saldo(c),
        const SizedBox(height: 14),
        _campoBusca(c),
        if (_painel.marcadores.isNotEmpty) ...[const SizedBox(height: 12), _chips(c)],
        const SizedBox(height: 12),
        _segmentos(c),
        ..._lista2(c),
      ]),
    );
  }

  Widget _saldo(AfCores c) {
    final k = _painel.carteira;
    if (k == null || k.modo == 'plan') return const Align(alignment: Alignment.centerLeft, child: AfSelo('Plano ativo · acessos sem custo', tom: AfTom.ok));
    return AfCartao(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
      child: Row(children: [
        Container(
          width: 40,
          height: 40,
          decoration: BoxDecoration(color: c.surface2, borderRadius: BorderRadius.circular(12)),
          child: Icon(Icons.toll_outlined, size: 20, color: c.warn),
        ),
        const SizedBox(width: 12),
        Expanded(
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Text('${k.creditos} ${k.creditos == 1 ? 'crédito' : 'créditos'}',
                style: TextStyle(fontFamily: kFonte, fontSize: 14, fontWeight: FontWeight.w700, color: c.text)),
            const AfMiudo('Cada acesso pelo celular usa 1, sem limite de tempo'),
          ]),
        ),
      ]),
    );
  }

  Widget _campoBusca(AfCores c) => Container(
        height: 48,
        padding: const EdgeInsets.symmetric(horizontal: 14),
        decoration: BoxDecoration(color: c.surface, borderRadius: BorderRadius.circular(14), border: Border.all(color: c.line)),
        child: Row(children: [
          Icon(Icons.search_rounded, size: 20, color: c.muted),
          const SizedBox(width: 10),
          Expanded(
            child: TextField(
              controller: _busca,
              style: TextStyle(fontFamily: kFonte, fontSize: 15, color: c.text),
              decoration: InputDecoration(
                isCollapsed: true,
                filled: false,
                border: InputBorder.none,
                enabledBorder: InputBorder.none,
                focusedBorder: InputBorder.none,
                hintText: 'Buscar por nome, ID, cliente ou CNPJ',
                hintStyle: TextStyle(fontFamily: kFonte, fontSize: 15, color: c.muted),
              ),
            ),
          ),
          if (_busca.text.isNotEmpty) GestureDetector(onTap: _busca.clear, child: Icon(Icons.close_rounded, size: 18, color: c.muted)),
        ]),
      );

  Widget _chips(AfCores c) => SizedBox(
        height: 32,
        child: ListView(scrollDirection: Axis.horizontal, children: [
          for (final m in _painel.marcadores)
            Padding(
              padding: const EdgeInsets.only(right: 8),
              child: _Chip(
                texto: m.rotulo,
                cor: corDoMarcador(context, m.cor),
                ligado: _filtro.contains(m.id),
                onTap: () => setState(() => _filtro.contains(m.id) ? _filtro.remove(m.id) : _filtro.add(m.id)),
              ),
            ),
        ]),
      );

  Widget _segmentos(AfCores c) {
    Widget b(_Lista l, String t) => Expanded(
          child: GestureDetector(
            onTap: () => setState(() => _lista = l),
            child: AnimatedContainer(
              duration: const Duration(milliseconds: 150),
              height: 34,
              alignment: Alignment.center,
              decoration: BoxDecoration(
                color: _lista == l ? c.surface2 : Colors.transparent,
                borderRadius: BorderRadius.circular(9),
                border: Border.all(color: _lista == l ? c.line2 : Colors.transparent),
              ),
              child: Text(t, style: TextStyle(fontFamily: kFonte, fontSize: 13, fontWeight: FontWeight.w700, color: _lista == l ? c.text : c.muted)),
            ),
          ),
        );
    return Container(
      padding: const EdgeInsets.all(3),
      decoration: BoxDecoration(color: c.surface, borderRadius: BorderRadius.circular(12), border: Border.all(color: c.line)),
      child: Row(children: [b(_Lista.todos, 'Todos'), b(_Lista.recentes, 'Recentes'), b(_Lista.favoritos, 'Favoritos')]),
    );
  }

  bool _bate(ComputadorEmpresa d, String q, String digitos) {
    if (q.isEmpty) return true;
    final marcadores = (_painel.marcacoes[d.id] ?? const <String>{})
        .map((id) => _painel.marcadores.where((m) => m.id == id).map((m) => m.rotulo.toLowerCase()).firstOrNull ?? '');
    if ([d.nome, _nomeCliente(d), ...marcadores].any((t) => t.toLowerCase().contains(q))) return true;
    if (digitos.isNotEmpty && d.rustdeskId.contains(digitos)) return true;
    // CNPJ/CPF: só os dígitos, a partir de 3, como no desktop.
    return digitos.length >= 3 && (d.documento ?? '').replaceAll(RegExp(r'\D'), '').contains(digitos);
  }

  List<Widget> _lista2(AfCores c) {
    final q = _busca.text.trim().toLowerCase();
    final digitos = q.replaceAll(RegExp(r'\D'), '');
    final lista = _painel.computadores.where((d) {
      if (_lista == _Lista.favoritos && !_favoritos.contains(d.rustdeskId)) return false;
      if (_lista == _Lista.recentes && !_recentes.contains(d.rustdeskId)) return false;
      if (_filtro.isNotEmpty && !(_painel.marcacoes[d.id] ?? const <String>{}).any(_filtro.contains)) return false;
      return _bate(d, q, digitos);
    }).toList();

    final grupos = <String, List<ComputadorEmpresa>>{};
    for (final d in lista) {
      grupos.putIfAbsent(_nomeCliente(d), () => []).add(d);
    }
    final chaves = grupos.keys.toList()
      ..sort((a, b) => a.isEmpty ? 1 : (b.isEmpty ? -1 : a.toLowerCase().compareTo(b.toLowerCase())));

    final idAvulso = (digitos.length == 9 || digitos.length == 10) && digitos == q.replaceAll(' ', '') && !_painel.computadores.any((d) => d.rustdeskId == digitos);

    final out = <Widget>[];
    if (idAvulso) {
      out
        ..add(const SizedBox(height: 14))
        ..add(AfLista([
          AfLinha(
            icone: Icons.arrow_forward_rounded,
            titulo: 'Conectar ao ID ${formatarId(digitos)}',
            sub: 'Computador fora da lista da empresa',
            fim: Icon(Icons.chevron_right_rounded, color: c.muted),
            onTap: () => connect(context, digitos),
          ),
        ]));
    }
    if (_painel.carregando && _painel.computadores.isEmpty) {
      out.add(const Padding(padding: EdgeInsets.all(32), child: Center(child: CircularProgressIndicator())));
      return out;
    }
    if (lista.isEmpty && !idAvulso) {
      out
        ..add(const SizedBox(height: 14))
        ..add(AfCartao(
          child: Column(children: [
            Text('Nada encontrado', style: TextStyle(fontFamily: kFonte, fontSize: 14, fontWeight: FontWeight.w700, color: c.text)),
            const SizedBox(height: 4),
            AfMiudo(
                _lista == _Lista.favoritos ? 'Marque a estrela de um computador para ele aparecer aqui.' : 'Tente outro nome, ID, CNPJ ou marcador.',
                alinhar: TextAlign.center),
          ]),
        ));
      return out;
    }
    for (final k in chaves) {
      final ds = grupos[k]!;
      final doc = formatarDocumento(ds.first.documento);
      out
        ..add(const SizedBox(height: 16))
        ..add(Padding(
          padding: const EdgeInsets.symmetric(horizontal: 2),
          child: Row(children: [
            Container(
              width: 28,
              height: 28,
              decoration: BoxDecoration(color: c.primary.withOpacity(0.14), borderRadius: BorderRadius.circular(8)),
              child: Icon(Icons.apartment_outlined, size: 16, color: c.accentText),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                Text(k.isEmpty ? 'Sem cliente vinculado' : k,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                        fontFamily: kFonte, fontSize: 13.5, fontWeight: k.isEmpty ? FontWeight.w500 : FontWeight.w700, color: k.isEmpty ? c.muted : c.text)),
                if (doc.isNotEmpty) Text(doc, style: TextStyle(fontFamily: kFonteMono, fontSize: 11, color: c.muted)),
              ]),
            ),
            Text('${ds.where((d) => d.online).length} de ${ds.length} ligados', style: TextStyle(fontFamily: kFonte, fontSize: 12.5, color: c.muted)),
          ]),
        ))
        ..add(const SizedBox(height: 8))
        ..add(AfLista([for (final d in ds) _linha(c, d)]));
    }
    return out;
  }

  Widget _linha(AfCores c, ComputadorEmpresa d) {
    final ids = _painel.marcacoes[d.id] ?? const <String>{};
    final cores = [for (final m in _painel.marcadores) if (ids.contains(m.id)) corDoMarcador(context, m.cor)];
    final sub = d.online
        ? (d.status == 'atendimento' ? '${formatarId(d.rustdeskId)} · em atendimento' : formatarId(d.rustdeskId))
        : '${formatarId(d.rustdeskId)} · desligado';
    return AfLinha(
      lider: _IconePc(d),
      titulo: d.nome,
      sub: sub,
      onTap: () => _abrirComputador(d),
      fim: Row(mainAxisSize: MainAxisSize.min, children: [
        for (final cor in cores.take(4))
          Container(margin: const EdgeInsets.only(left: 3), width: 7, height: 7, decoration: BoxDecoration(color: cor, shape: BoxShape.circle)),
        if (_favoritos.contains(d.rustdeskId)) ...[const SizedBox(width: 8), Icon(Icons.star_rounded, size: 17, color: c.warn)],
      ]),
    );
  }

  // ------------------------------------------------------------------ folha do computador

  void _abrirComputador(ComputadorEmpresa d) {
    final c = AfCores.of(context);
    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      backgroundColor: c.side,
      shape: RoundedRectangleBorder(borderRadius: const BorderRadius.vertical(top: Radius.circular(26)), side: BorderSide(color: c.line2)),
      builder: (ctx) => StatefulBuilder(builder: (ctx, setFolha) {
        final k = _painel.carteira;
        final credito = k != null && k.modo != 'plan' && _painel.perfil?.empresaId != null;
        final ids = _painel.marcacoes[d.id] ?? const <String>{};
        final doc = formatarDocumento(d.documento);
        final fav = _favoritos.contains(d.rustdeskId);
        return SafeArea(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(18, 10, 18, 18),
            child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.stretch, children: [
              Center(child: Container(width: 40, height: 4, decoration: BoxDecoration(color: c.line2, borderRadius: BorderRadius.circular(4)))),
              const SizedBox(height: 14),
              Row(children: [
                _IconePc(d),
                const SizedBox(width: 12),
                Expanded(
                  child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                    Text(d.nome, maxLines: 1, overflow: TextOverflow.ellipsis,
                        style: TextStyle(fontFamily: kFonte, fontSize: 17, fontWeight: FontWeight.w800, color: c.text)),
                    Text(formatarId(d.rustdeskId), style: TextStyle(fontFamily: kFonteMono, fontSize: 12, color: c.muted)),
                  ]),
                ),
                AfBotaoIcone(fav ? Icons.star_rounded : Icons.star_outline_rounded, dica: 'Favorito', onTap: () async {
                  await _alternarFavorito(d);
                  setFolha(() {});
                }),
              ]),
              const SizedBox(height: 14),
              AfCartao(
                padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
                child: Row(children: [
                  Icon(Icons.apartment_outlined, size: 16, color: _nomeCliente(d).isEmpty ? c.muted : c.accentText),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                      Text(_nomeCliente(d).isEmpty ? 'Sem cliente vinculado' : _nomeCliente(d),
                          style: TextStyle(fontFamily: kFonte, fontSize: 13.5, fontWeight: FontWeight.w700, color: _nomeCliente(d).isEmpty ? c.muted : c.text)),
                      if (doc.isNotEmpty) Text(doc, style: TextStyle(fontFamily: kFonteMono, fontSize: 11, color: c.muted)),
                      if (_painel.empresas.length > 1 && d.empresaNome.isNotEmpty)
                        Text('Empresa: ${d.empresaNome}', style: TextStyle(fontFamily: kFonte, fontSize: 11, color: c.muted)),
                    ]),
                  ),
                ]),
              ),
              const SizedBox(height: 10),
              AfCartao(
                padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
                child: Column(children: [
                  Row(children: [
                    const Expanded(child: AfMiudo('Situação')),
                    d.online
                        ? AfSelo(d.status == 'atendimento' ? 'Em atendimento' : 'Ligado', tom: d.status == 'atendimento' ? AfTom.live : AfTom.ok)
                        : const AfSelo('Desligado', tom: AfTom.off),
                  ]),
                  if (ids.isNotEmpty) ...[
                    const SizedBox(height: 10),
                    Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
                      const Padding(padding: EdgeInsets.only(top: 4), child: AfMiudo('Marcadores')),
                      const SizedBox(width: 12),
                      Expanded(
                        child: Wrap(alignment: WrapAlignment.end, spacing: 6, runSpacing: 6, children: [
                          for (final m in _painel.marcadores)
                            if (ids.contains(m.id)) _Chip(texto: m.rotulo, cor: corDoMarcador(context, m.cor), ligado: false, pequeno: true),
                        ]),
                      ),
                    ]),
                  ],
                  if (d.privado) ...[
                    const SizedBox(height: 10),
                    const Row(children: [Expanded(child: AfMiudo('Computador privado: a pessoa do outro lado precisa aceitar o acesso.'))]),
                  ],
                ]),
              ),
              if (credito) ...[
                const SizedBox(height: 10),
                Row(children: [
                  Icon(Icons.toll_outlined, size: 16, color: c.muted),
                  const SizedBox(width: 8),
                  Expanded(child: AfMiudo('Este acesso usa 1 crédito. Você tem ${k.creditos}.')),
                ]),
              ],
              const SizedBox(height: 14),
              AfBotao(d.online ? 'Conectar${credito ? ' · 1 crédito' : ''}' : 'Computador desligado',
                  icone: Icons.arrow_forward_rounded,
                  estilo: AfEstiloBotao.primario,
                  largo: true,
                  onTap: d.online ? () => _conectar(ctx, d) : null),
              const SizedBox(height: 8),
              AfBotao('Só arquivos${credito ? ' · 1 crédito' : ''}',
                  icone: Icons.folder_outlined, largo: true, onTap: d.online ? () => _conectar(ctx, d, arquivos: true) : null),
            ]),
          ),
        );
      }),
    );
  }

  /// Igual ao desktop e ao botão Conectar do painel. No celular a conta medida sempre usa
  /// crédito: se o painel pedir para escolher, a escolha já está feita.
  Future<void> _conectar(BuildContext folha, ComputadorEmpresa d, {bool arquivos = false}) async {
    Navigator.of(folha).pop();
    final medida = _painel.carteira?.medida == true && _painel.perfil?.empresaId != null;
    final c = AfCores.of(context);
    showDialog(
      context: context,
      barrierDismissible: false,
      builder: (_) => Center(
        child: Container(
          padding: const EdgeInsets.all(22),
          decoration: BoxDecoration(color: c.side, borderRadius: BorderRadius.circular(20), border: Border.all(color: c.line2)),
          child: Column(mainAxisSize: MainAxisSize.min, children: [
            const AfSelo('Conectando', tom: AfTom.live, pulsando: true),
            const SizedBox(height: 10),
            Text(d.nome, style: TextStyle(fontFamily: kFonte, fontSize: 17, fontWeight: FontWeight.w800, color: c.text, decoration: TextDecoration.none)),
          ]),
        ),
      ),
    );
    void fechar() {
      if (mounted) Navigator.of(context, rootNavigator: true).pop();
    }

    try {
      Acesso acesso;
      try {
        acesso = await _painel.pedirAcesso(d.id, origem: medida ? 'credit' : null);
      } on EscolhaCobranca {
        acesso = await _painel.pedirAcesso(d.id, origem: 'credit');
      }
      fechar();
      if (!mounted) return;
      if (acesso.privado && acesso.senha == null) showToast('Computador privado: a pessoa do outro lado precisa aceitar o acesso.');
      connect(context, acesso.rustdeskId, password: acesso.senha, isFileTransfer: arquivos);
      unawaited(_carregarLocais());
    } on ErroPainel catch (e) {
      fechar();
      showToast(e.mensagem);
    } catch (_) {
      fechar();
      showToast('Não foi possível falar com o painel. Confira a internet e tente de novo.');
    }
  }

  Future<void> _entrar(BuildContext context) async {
    final ok = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      backgroundColor: AfCores.of(context).side,
      shape: RoundedRectangleBorder(borderRadius: const BorderRadius.vertical(top: Radius.circular(26)), side: BorderSide(color: AfCores.of(context).line2)),
      builder: (_) => const _FolhaLogin(),
    );
    if (ok == true) {
      showToast('Olá, ${_painel.perfil!.nome.split(' ').first}!');
      unawaited(_carregarLocais());
    }
  }
}

class _FolhaLogin extends StatefulWidget {
  const _FolhaLogin();

  @override
  State<_FolhaLogin> createState() => _FolhaLoginState();
}

class _FolhaLoginState extends State<_FolhaLogin> {
  final _email = TextEditingController();
  final _senha = TextEditingController();
  bool _enviando = false;
  String? _erro;

  @override
  void dispose() {
    _email.dispose();
    _senha.dispose();
    super.dispose();
  }

  Future<void> _entrar() async {
    if (_email.text.trim().isEmpty || _senha.text.isEmpty) return setState(() => _erro = 'Preencha o e-mail e a senha.');
    setState(() {
      _enviando = true;
      _erro = null;
    });
    try {
      await Painel.instancia.entrar(_email.text, _senha.text);
      if (mounted) Navigator.of(context).pop(true);
    } on ErroPainel catch (e) {
      setState(() => _erro = e.mensagem);
    } catch (_) {
      setState(() => _erro = 'Não foi possível entrar agora. Confira a internet e tente de novo.');
    } finally {
      if (mounted) setState(() => _enviando = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = AfCores.of(context);
    InputDecoration deco(String dica, IconData icone) => InputDecoration(
          hintText: dica,
          prefixIcon: Icon(icone, size: 20, color: c.muted),
          filled: true,
          fillColor: c.surface,
          contentPadding: const EdgeInsets.symmetric(vertical: 14),
          border: OutlineInputBorder(borderRadius: BorderRadius.circular(14), borderSide: BorderSide(color: c.line)),
          enabledBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(14), borderSide: BorderSide(color: c.line)),
          focusedBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(14), borderSide: BorderSide(color: c.primary, width: 1.5)),
        );
    return Padding(
      padding: EdgeInsets.fromLTRB(18, 10, 18, 18 + MediaQuery.of(context).viewInsets.bottom),
      child: SafeArea(
        child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.stretch, children: [
          Center(child: Container(width: 40, height: 4, decoration: BoxDecoration(color: c.line2, borderRadius: BorderRadius.circular(4)))),
          const SizedBox(height: 14),
          Text('Entrar com a conta do painel', style: TextStyle(fontFamily: kFonte, fontSize: 19, fontWeight: FontWeight.w800, color: c.text)),
          const SizedBox(height: 4),
          const AfMiudo('O mesmo e-mail e senha que você usa no painel do AcessoFast.'),
          const SizedBox(height: 14),
          TextField(
            controller: _email,
            keyboardType: TextInputType.emailAddress,
            autofillHints: const [AutofillHints.email],
            style: TextStyle(fontFamily: kFonte, fontSize: 15, color: c.text),
            decoration: deco('E-mail', Icons.person_outline_rounded),
          ),
          const SizedBox(height: 10),
          TextField(
            controller: _senha,
            obscureText: true,
            autofillHints: const [AutofillHints.password],
            onSubmitted: (_) => _entrar(),
            style: TextStyle(fontFamily: kFonte, fontSize: 15, color: c.text),
            decoration: deco('Senha', Icons.lock_outline_rounded),
          ),
          if (_erro != null) ...[
            const SizedBox(height: 10),
            Text(_erro!, style: TextStyle(fontFamily: kFonte, fontSize: 13, color: c.dangerText)),
          ],
          const SizedBox(height: 14),
          AfBotao(_enviando ? 'Entrando…' : 'Entrar', estilo: AfEstiloBotao.primario, largo: true, onTap: _enviando ? null : _entrar),
        ]),
      ),
    );
  }
}

class _Avatar extends StatelessWidget {
  final String nome;
  const _Avatar(this.nome);

  @override
  Widget build(BuildContext context) {
    final ini = nome.split(RegExp(r'\s+')).where((p) => p.isNotEmpty).take(2).map((p) => p[0].toUpperCase()).join();
    return Container(
      width: 36,
      height: 36,
      alignment: Alignment.center,
      decoration: const BoxDecoration(shape: BoxShape.circle, gradient: LinearGradient(colors: [Color(0xFF2C67FA), Color(0xFF6B8CFF)])),
      child: Text(ini, style: const TextStyle(fontFamily: kFonte, fontSize: 13, fontWeight: FontWeight.w800, color: Colors.white)),
    );
  }
}

class _Arte extends StatelessWidget {
  final IconData icone;
  final Color cor;
  const _Arte(this.icone, this.cor);

  @override
  Widget build(BuildContext context) => Container(
        width: 96,
        height: 96,
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(28),
          border: Border.all(color: cor.withOpacity(0.3)),
          gradient: RadialGradient(center: const Alignment(-0.4, -0.6), colors: [cor.withOpacity(0.3), cor.withOpacity(0.08)]),
        ),
        child: Icon(icone, size: 42, color: cor),
      );
}

class _IconePc extends StatelessWidget {
  final ComputadorEmpresa d;
  const _IconePc(this.d);

  @override
  Widget build(BuildContext context) {
    final c = AfCores.of(context);
    return SizedBox(
      width: 42,
      height: 42,
      child: Stack(children: [
        Container(
          width: 40,
          height: 40,
          decoration: BoxDecoration(color: c.surface2, borderRadius: BorderRadius.circular(12)),
          child: Icon(iconeDoSistema(d.os), size: 20, color: c.text2),
        ),
        Positioned(
          right: 0,
          bottom: 0,
          child: Container(
            width: 12,
            height: 12,
            decoration: BoxDecoration(
              shape: BoxShape.circle,
              color: d.status == 'atendimento' ? c.cyan : (d.online ? c.ok : c.muted),
              border: Border.all(color: c.surface, width: 2),
            ),
          ),
        ),
      ]),
    );
  }
}

class _Chip extends StatelessWidget {
  final String texto;
  final Color cor;
  final bool ligado;
  final bool pequeno;
  final VoidCallback? onTap;
  const _Chip({required this.texto, required this.cor, required this.ligado, this.pequeno = false, this.onTap});

  @override
  Widget build(BuildContext context) {
    final c = AfCores.of(context);
    return GestureDetector(
      onTap: onTap,
      child: Container(
        height: pequeno ? 26 : 32,
        padding: const EdgeInsets.symmetric(horizontal: 12),
        decoration: BoxDecoration(
          color: ligado ? c.primary.withOpacity(0.18) : c.surface,
          borderRadius: BorderRadius.circular(99),
          border: Border.all(color: ligado ? c.primary.withOpacity(0.5) : c.line),
        ),
        child: Row(mainAxisSize: MainAxisSize.min, children: [
          Container(width: 8, height: 8, decoration: BoxDecoration(color: cor, shape: BoxShape.circle)),
          const SizedBox(width: 6),
          Text(texto, style: TextStyle(fontFamily: kFonte, fontSize: 12.5, fontWeight: FontWeight.w600, color: ligado ? c.text : c.text2)),
        ]),
      ),
    );
  }
}
