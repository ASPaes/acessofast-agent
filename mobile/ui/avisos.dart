// CÓPIA de acessofast-desktop/ui/lib/avisos.dart: mesmo código no app do celular. Mudou lá, copie para cá
// (e vice-versa).
//
// Conteúdo dos avisos do RustDesk (msgBox: erro de conexão, senha errada, aguardando aceite…)
// no desenho do AcessoFast. O msgboxContent do common.dart passa a devolver este widget (ver
// patches/ em preparar.sh); a caixa e os botões vêm do tema (tema.dart).
import 'dart:math' as math;
import 'dart:ui' show ImageFilter;

import 'package:flutter/material.dart';
import 'package:flutter_hbb/common.dart' show translate;

import 'tema.dart';

/// Mesma regra do translateText do msgboxContent original: "Failed to x: y" traduz por partes e
/// "algo_tip resto" traduz a dica e o resto separados.
String _traduzir(String texto) {
  if (texto.indexOf('Failed') == 0 && texto.indexOf(': ') > 0) {
    return texto.split(': ').map(translate).join(': ');
  }
  final palavras = texto.split(' ');
  if (palavras.length > 1 && palavras[0].endsWith('_tip')) {
    return '${translate(palavras[0])} ${translate(texto.substring(palavras[0].length + 1))}';
  }
  return translate(texto);
}

/// Quantos avisos de erro estão abertos nesta janela: o fundo da sessão sem imagem fica com
/// alertas vermelhos enquanto houver algum.
final avisosDeErroAbertos = ValueNotifier<int>(0);

bool _ehErro(String tipo) => tipo.contains('error') || tipo == 're-input-password';

class AfConteudoAviso extends StatefulWidget {
  final String tipo;
  final String titulo;
  final String texto;
  const AfConteudoAviso({super.key, required this.tipo, required this.titulo, required this.texto});

  @override
  State<AfConteudoAviso> createState() => _AfConteudoAvisoState();
}

class _AfConteudoAvisoState extends State<AfConteudoAviso> {
  String get tipo => widget.tipo;
  String get titulo => widget.titulo;
  String get texto => widget.texto;

  @override
  void initState() {
    super.initState();
    if (_ehErro(tipo)) WidgetsBinding.instance.addPostFrameCallback((_) => avisosDeErroAbertos.value++);
  }

  @override
  void dispose() {
    if (_ehErro(tipo)) WidgetsBinding.instance.addPostFrameCallback((_) => avisosDeErroAbertos.value = math.max(0, avisosDeErroAbertos.value - 1));
    super.dispose();
  }

  (IconData, Color)? _icone(AfCores c) {
    if (tipo.contains('error') || tipo == 're-input-password') return (Icons.error_outline, c.danger);
    if (tipo.contains('success')) return (Icons.check_circle_outline, c.ok);
    if (tipo == 'wait-uac' || tipo == 'wait-remote-accept-nook') return (Icons.hourglass_top, c.warn);
    if (tipo == 'on-uac' || tipo == 'on-foreground-elevated') return (Icons.admin_panel_settings_outlined, c.warn);
    if (tipo.contains('info')) return (Icons.info_outline, c.accentText);
    return null;
  }

  @override
  Widget build(BuildContext context) {
    final c = AfCores.of(context);
    final icone = _icone(c);
    final t = translate(titulo);
    final corpo = _traduzir(texto);
    return ConstrainedBox(
      constraints: const BoxConstraints(minWidth: 300, maxWidth: 380),
      child: Padding(
        padding: const EdgeInsets.only(bottom: 4),
        child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
          if (icone != null) ...[
            Container(
              width: 38,
              height: 38,
              decoration: BoxDecoration(color: icone.$2.withOpacity(0.14), borderRadius: BorderRadius.circular(10)),
              child: Icon(icone.$1, size: 21, color: icone.$2),
            ),
            const SizedBox(width: 14),
          ],
          Expanded(
            child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              if (t.trim().isNotEmpty) ...[
                Padding(
                  padding: const EdgeInsets.only(top: 1),
                  child: Text(t, style: TextStyle(fontFamily: kFonte, fontSize: 16, fontWeight: FontWeight.w700, color: c.text, height: 1.3)),
                ),
                const SizedBox(height: 5),
              ],
              if (corpo.trim().isNotEmpty)
                SelectableText(corpo, style: TextStyle(fontFamily: kFonte, fontSize: 13.5, color: c.text2, height: 1.45)),
            ]),
          ),
        ]),
      ),
    );
  }
}

// ==================================================================== fundo da sessão sem imagem

/// Fundo da sessão enquanto não há imagem do outro computador (conectando, aguardando aceite,
/// erro): ícones grandes, desfocados, flutuando devagar. Pedido do Ryan (01/10): "uns alertas de
/// fundo desfocados, com uma leve animação, para não ficar seco". Com erro na tela são alertas
/// vermelhos; conectando, monitores azuis (alerta a cada conexão assustaria o técnico).
class AfFundoConexao extends StatefulWidget {
  const AfFundoConexao({super.key});

  @override
  State<AfFundoConexao> createState() => _AfFundoConexaoState();
}

class _AfFundoConexaoState extends State<AfFundoConexao> with SingleTickerProviderStateMixin {
  late final _a = AnimationController(vsync: this, duration: const Duration(seconds: 24))..repeat();

  // (x, y, tamanho, fase): posições fixas, espalhadas, para o desenho não pular entre conexões.
  static const _icones = [
    (0.10, 0.18, 150.0, 0.0),
    (0.82, 0.12, 110.0, 0.6),
    (0.28, 0.72, 120.0, 1.7),
    (0.67, 0.62, 170.0, 2.9),
    (0.92, 0.80, 90.0, 4.1),
    (0.48, 0.30, 80.0, 3.3),
    (0.05, 0.88, 100.0, 5.2),
    (0.58, 0.95, 70.0, 0.9),
  ];

  @override
  void dispose() {
    _a.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final c = AfCores.escuro;
    return IgnorePointer(
      child: RepaintBoundary(
        child: ValueListenableBuilder<int>(
          valueListenable: avisosDeErroAbertos,
          builder: (_, erros, __) {
            final erro = erros > 0;
            final cor = erro ? const Color(0xFFFF6370) : c.primary;
            return AnimatedContainer(
              duration: const Duration(milliseconds: 600),
              decoration: BoxDecoration(
                gradient: RadialGradient(
                  center: Alignment.center,
                  radius: 1.1,
                  colors: [Color.lerp(c.bg, cor, 0.10)!, c.bg],
                ),
              ),
              child: LayoutBuilder(builder: (_, box) {
                return ImageFiltered(
                  imageFilter: ImageFilter.blur(sigmaX: 5, sigmaY: 5),
                  child: AnimatedBuilder(
                    animation: _a,
                    builder: (_, __) {
                      final t = _a.value * 2 * math.pi;
                      return Stack(children: [
                        for (final (x, y, tam, fase) in _icones)
                          Positioned(
                            left: x * box.maxWidth - tam / 2 + math.sin(t + fase) * 18,
                            top: y * box.maxHeight - tam / 2 + math.cos(t * 0.8 + fase) * 26,
                            child: Transform.rotate(
                              angle: math.sin(t * 0.5 + fase) * 0.12,
                              child: Opacity(
                                opacity: 0.42 + 0.10 * math.sin(t + fase * 2),
                                child: _IconeFundo(tamanho: tam, cor: cor, erro: erro),
                              ),
                            ),
                          ),
                      ]);
                    },
                  ),
                );
              }),
            );
          },
        ),
      ),
    );
  }
}

/// Círculo com o símbolo, no estilo do alerta que o Ryan mandou (círculo cheio, sinal claro).
class _IconeFundo extends StatelessWidget {
  final double tamanho;
  final Color cor;
  final bool erro;
  const _IconeFundo({required this.tamanho, required this.cor, required this.erro});

  @override
  Widget build(BuildContext context) => AnimatedContainer(
        duration: const Duration(milliseconds: 600),
        width: tamanho,
        height: tamanho,
        decoration: BoxDecoration(color: cor, shape: BoxShape.circle),
        child: Icon(
          erro ? Icons.priority_high_rounded : Icons.desktop_windows_rounded,
          size: tamanho * (erro ? 0.62 : 0.5),
          color: const Color(0xFFE5E8EE),
        ),
      );
}
