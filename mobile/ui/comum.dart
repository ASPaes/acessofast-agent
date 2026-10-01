// Peças da interface do celular, no desenho do protótipo aprovado
// (acessofast-desktop/prototipo-mobile/app.css). Copiado pelo CI para flutter/lib/acessofast/ui/.
import 'package:flutter/material.dart';

import 'tema.dart';

/// Cartão de fundo `surface` com borda fina (.card).
class AfCartao extends StatelessWidget {
  final Widget child;
  final EdgeInsets padding;
  final bool destaque;
  final Color? borda;
  const AfCartao({super.key, required this.child, this.padding = const EdgeInsets.all(16), this.destaque = false, this.borda});

  @override
  Widget build(BuildContext context) {
    final c = AfCores.of(context);
    return Container(
      width: double.infinity,
      padding: padding,
      decoration: BoxDecoration(
        color: c.surface,
        borderRadius: BorderRadius.circular(18),
        border: Border.all(color: borda ?? c.line),
        // .card.hero: brilho azul no canto de cima.
        gradient: destaque
            ? RadialGradient(center: const Alignment(1, -1), radius: 1.1, colors: [c.glow, c.surface], stops: const [0, 0.7])
            : null,
      ),
      child: child,
    );
  }
}

/// Rótulo pequeno em caixa alta (.lbl).
class AfRotulo extends StatelessWidget {
  final String texto;
  const AfRotulo(this.texto, {super.key});

  @override
  Widget build(BuildContext context) => Text(texto.toUpperCase(),
      style: TextStyle(fontFamily: kFonte, fontSize: 11, fontWeight: FontWeight.w700, letterSpacing: 1, color: AfCores.of(context).muted));
}

enum AfTom { ok, live, off, warn }

/// Selo arredondado com bolinha (.pill).
class AfSelo extends StatefulWidget {
  final String texto;
  final AfTom tom;
  final bool pulsando;
  const AfSelo(this.texto, {super.key, required this.tom, this.pulsando = false});

  @override
  State<AfSelo> createState() => _AfSeloState();
}

class _AfSeloState extends State<AfSelo> with SingleTickerProviderStateMixin {
  late final AnimationController _a = AnimationController(vsync: this, duration: const Duration(milliseconds: 1600));

  @override
  void initState() {
    super.initState();
    if (widget.pulsando) _a.repeat(reverse: true);
  }

  @override
  void didUpdateWidget(AfSelo old) {
    super.didUpdateWidget(old);
    if (widget.pulsando && !_a.isAnimating) _a.repeat(reverse: true);
    if (!widget.pulsando && _a.isAnimating) _a.stop();
  }

  @override
  void dispose() {
    _a.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final c = AfCores.of(context);
    final cor = switch (widget.tom) { AfTom.ok => c.ok, AfTom.live => c.cyan, AfTom.warn => c.warn, AfTom.off => c.muted };
    final fundo = widget.tom == AfTom.off ? c.surface2 : cor.withOpacity(0.13);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 5),
      decoration: BoxDecoration(color: fundo, borderRadius: BorderRadius.circular(99)),
      child: Row(mainAxisSize: MainAxisSize.min, children: [
        FadeTransition(
          opacity: widget.pulsando ? Tween(begin: 1.0, end: 0.35).animate(_a) : const AlwaysStoppedAnimation(1),
          child: Container(width: 7, height: 7, decoration: BoxDecoration(color: cor, shape: BoxShape.circle)),
        ),
        const SizedBox(width: 6),
        Text(widget.texto, style: TextStyle(fontFamily: kFonte, fontSize: 12, fontWeight: FontWeight.w700, color: cor)),
      ]),
    );
  }
}

/// Botão quadrado de ícone (.ibtn).
class AfBotaoIcone extends StatelessWidget {
  final IconData icone;
  final VoidCallback? onTap;
  final String? dica;
  const AfBotaoIcone(this.icone, {super.key, this.onTap, this.dica});

  @override
  Widget build(BuildContext context) {
    final c = AfCores.of(context);
    final b = Material(
      color: c.surface2,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12), side: BorderSide(color: c.line)),
      child: InkWell(
        borderRadius: BorderRadius.circular(12),
        onTap: onTap,
        child: SizedBox(width: 40, height: 40, child: Icon(icone, size: 20, color: c.text2)),
      ),
    );
    return dica == null ? b : Tooltip(message: dica!, child: b);
  }
}

enum AfEstiloBotao { primario, normal, perigo, texto }

/// Botão grande (.btn), 48 px de altura; `pequeno` = .btn.sm.
class AfBotao extends StatelessWidget {
  final String texto;
  final IconData? icone;
  final VoidCallback? onTap;
  final AfEstiloBotao estilo;
  final bool pequeno;
  final bool largo;
  const AfBotao(this.texto, {super.key, this.icone, this.onTap, this.estilo = AfEstiloBotao.normal, this.pequeno = false, this.largo = false});

  @override
  Widget build(BuildContext context) {
    final c = AfCores.of(context);
    final (Color fundo, Color borda, Color frente) = switch (estilo) {
      AfEstiloBotao.primario => (c.primary, c.primary, Colors.white),
      AfEstiloBotao.perigo => (c.danger.withOpacity(0.14), c.danger.withOpacity(0.35), c.dangerText),
      AfEstiloBotao.texto => (Colors.transparent, Colors.transparent, c.accentText),
      AfEstiloBotao.normal => (c.surface2, c.line2, c.text),
    };
    final raio = BorderRadius.circular(pequeno ? 10 : 14);
    return Opacity(
      opacity: onTap == null ? 0.45 : 1,
      child: Material(
        color: fundo,
        shape: RoundedRectangleBorder(borderRadius: raio, side: BorderSide(color: borda)),
        child: InkWell(
          borderRadius: raio,
          onTap: onTap,
          child: Container(
            height: pequeno ? 36 : 48,
            width: largo ? double.infinity : null,
            padding: EdgeInsets.symmetric(horizontal: pequeno ? 12 : 18),
            alignment: Alignment.center,
            child: Row(mainAxisSize: MainAxisSize.min, children: [
              if (icone != null) ...[Icon(icone, size: pequeno ? 16 : 18, color: frente), const SizedBox(width: 8)],
              Text(texto, style: TextStyle(fontFamily: kFonte, fontSize: pequeno ? 13 : 15, fontWeight: FontWeight.w700, color: frente)),
            ]),
          ),
        ),
      ),
    );
  }
}

/// Lista com linhas separadas por traço fino (.list).
class AfLista extends StatelessWidget {
  final List<Widget> linhas;
  const AfLista(this.linhas, {super.key});

  @override
  Widget build(BuildContext context) {
    final c = AfCores.of(context);
    return Container(
      decoration: BoxDecoration(color: c.surface, borderRadius: BorderRadius.circular(18), border: Border.all(color: c.line)),
      clipBehavior: Clip.antiAlias,
      child: Column(children: [
        for (var i = 0; i < linhas.length; i++) ...[
          if (i > 0) Divider(height: 1, thickness: 1, color: c.line),
          linhas[i],
        ],
      ]),
    );
  }
}

enum AfTomIcone { normal, ok, warn }

/// Linha de lista (.li): ícone num quadrado, título, subtítulo e um final opcional.
class AfLinha extends StatelessWidget {
  final IconData? icone;
  final Widget? lider;
  final AfTomIcone tomIcone;
  final String titulo;
  final String? sub;
  final Widget? fim;
  final VoidCallback? onTap;
  final Color? corTitulo;
  const AfLinha({super.key, this.icone, this.lider, this.tomIcone = AfTomIcone.normal, required this.titulo, this.sub, this.fim, this.onTap, this.corTitulo});

  @override
  Widget build(BuildContext context) {
    final c = AfCores.of(context);
    final (Color corIc, Color fundoIc) = switch (tomIcone) {
      AfTomIcone.ok => (c.ok, c.ok.withOpacity(0.12)),
      AfTomIcone.warn => (c.warn, c.warn.withOpacity(0.12)),
      AfTomIcone.normal => (c.text2, c.surface2),
    };
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 13),
        child: Row(children: [
          if (lider != null) ...[lider!, const SizedBox(width: 12)],
          if (icone != null) ...[
            Container(
              width: 40,
              height: 40,
              decoration: BoxDecoration(color: fundoIc, borderRadius: BorderRadius.circular(12)),
              child: Icon(icone, size: 20, color: corIc),
            ),
            const SizedBox(width: 12),
          ],
          Expanded(
            child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              Text(titulo,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(fontFamily: kFonte, fontSize: 14, fontWeight: FontWeight.w700, color: corTitulo ?? c.text)),
              if (sub != null) ...[
                const SizedBox(height: 2),
                Text(sub!, maxLines: 2, overflow: TextOverflow.ellipsis, style: TextStyle(fontFamily: kFonte, fontSize: 12, color: c.muted)),
              ],
            ]),
          ),
          if (fim != null) ...[const SizedBox(width: 10), fim!],
        ]),
      ),
    );
  }
}

/// Cabeçalho de seção acima de uma lista (.sec).
class AfSecao extends StatelessWidget {
  final String titulo;
  final String? direita;
  const AfSecao(this.titulo, {super.key, this.direita});

  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.fromLTRB(2, 4, 2, 0),
        child: Row(children: [
          Expanded(child: AfRotulo(titulo)),
          if (direita != null) Text(direita!, style: TextStyle(fontFamily: kFonte, fontSize: 12.5, color: AfCores.of(context).muted)),
        ]),
      );
}

/// ID em destaque, mono, com o degradê azul do protótipo (.idbig).
class AfIdGrande extends StatelessWidget {
  final String id;
  final double tamanho;
  const AfIdGrande(this.id, {super.key, this.tamanho = 34});

  @override
  Widget build(BuildContext context) {
    final c = AfCores.of(context);
    return ShaderMask(
      blendMode: BlendMode.srcIn,
      shaderCallback: (r) => LinearGradient(colors: [c.idA, c.idB, c.idC], stops: const [0, 0.45, 1]).createShader(r),
      child: FittedBox(
        fit: BoxFit.scaleDown,
        alignment: Alignment.centerLeft,
        child: Text(id,
            style: TextStyle(
                fontFamily: kFonteMono, fontSize: tamanho, fontWeight: FontWeight.w700, height: 1.1, fontFeatures: const [FontFeature.tabularFigures()])),
      ),
    );
  }
}

/// Título grande da aba (.top h1).
class AfTitulo extends StatelessWidget {
  final String texto;
  final String? sub;
  final Widget? direita;
  const AfTitulo(this.texto, {super.key, this.sub, this.direita});

  @override
  Widget build(BuildContext context) {
    final c = AfCores.of(context);
    return Padding(
      padding: const EdgeInsets.only(top: 8, bottom: 2),
      child: Row(children: [
        Expanded(
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Text(texto, style: TextStyle(fontFamily: kFonte, fontSize: 24, fontWeight: FontWeight.w800, letterSpacing: -0.5, color: c.text)),
            if (sub != null) Text(sub!, style: TextStyle(fontFamily: kFonte, fontSize: 12, fontWeight: FontWeight.w600, color: c.muted)),
          ]),
        ),
        if (direita != null) direita!,
      ]),
    );
  }
}

/// Texto miúdo cinza (.small.muted).
class AfMiudo extends StatelessWidget {
  final String texto;
  final TextAlign? alinhar;
  const AfMiudo(this.texto, {super.key, this.alinhar});

  @override
  Widget build(BuildContext context) =>
      Text(texto, textAlign: alinhar, style: TextStyle(fontFamily: kFonte, fontSize: 12.5, height: 1.45, color: AfCores.of(context).muted));
}
