// Otto, o mascote do AcessoFast, para os apps (computador e celular). É o mesmo desenho do
// painel e do site (app-acessofast/src/components/otto/otto.tsx): um monitor de massinha branco
// com o rosto na tela e uma luzinha embaixo na cor do estado. Lá ele é SVG; aqui cada camada
// é um CustomPainter com a mesma geometria (viewBox 8 16 224 224), passada para Dart.
//
// Este arquivo é IGUAL no app do computador (acessofast-desktop/ui/lib) e no do celular
// (acessofast-agent/mobile/ui). Mudou o desenho no painel? Traga para cá e copie para o outro.
//
// Desempenho (a mesma lição do site, que travou com a primeira versão): o corpo, com os
// desfoques, é desenhado uma vez por estado numa camada própria (RepaintBoundary). O que se
// mexe (flutuar, piscar, acenar, pontinhos, brilhos, zz) move a camada inteira com Transform
// ou Opacity, sem redesenhar nada. Os enfeites param depois de algumas voltas; a piscada e a
// flutuação são eternas, como no painel.

import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';

enum OttoEstado { parado, ola, conectando, atendendo, aguardando, erro, concluido, offline }

enum _Olhos { pontos, feliz, traco, dormindo, olhando }

enum _Balao { pontos, cursor, pergunta, alerta, check }

class _Estilo {
  const _Estilo(this.olhos, {this.cor, this.tinta, this.balao});
  final _Olhos olhos;
  final Color? cor;
  final Color? tinta;
  final _Balao? balao;
}

const _estilos = <OttoEstado, _Estilo>{
  OttoEstado.parado: _Estilo(_Olhos.pontos),
  OttoEstado.ola: _Estilo(_Olhos.feliz),
  OttoEstado.conectando:
      _Estilo(_Olhos.olhando, cor: Color(0xFF64A4FF), tinta: Color(0xFF082457), balao: _Balao.pontos),
  OttoEstado.atendendo:
      _Estilo(_Olhos.pontos, cor: Color(0xFF2C67FA), tinta: Color(0xFFFFFFFF), balao: _Balao.cursor),
  OttoEstado.aguardando:
      _Estilo(_Olhos.pontos, cor: Color(0xFFF5B942), tinta: Color(0xFF3A2500), balao: _Balao.pergunta),
  OttoEstado.erro:
      _Estilo(_Olhos.traco, cor: Color(0xFFF05252), tinta: Color(0xFFFFFFFF), balao: _Balao.alerta),
  OttoEstado.concluido:
      _Estilo(_Olhos.feliz, cor: Color(0xFF31C48D), tinta: Color(0xFF04291C), balao: _Balao.check),
  OttoEstado.offline: _Estilo(_Olhos.dormindo, cor: Color(0xFF7C8AA5)),
};

// ---- geometria (a mesma do otto.tsx) ----

double _r1(double n) => (n * 10).roundToDouble() / 10;

/// Superelipse: n = 2 é elipse; quanto maior, mais quadrada.
List<Offset> _superelipse(double cx, double cy, double rx, double ry, double n, {int pontos = 80}) {
  final e = 2 / n;
  return List.generate(pontos, (i) {
    final t = ((i + 0.5) / pontos) * 2 * math.pi;
    final c = math.cos(t), s = math.sin(t);
    return Offset(
      _r1(cx + rx * c.sign * math.pow(c.abs(), e)),
      _r1(cy + ry * s.sign * math.pow(s.abs(), e)),
    );
  });
}

/// Contorno suave por pontos médios: cada ponto vira o controle de uma quadrática.
Path _suave(List<Offset> pts) {
  Offset meio(Offset a, Offset b) => Offset((a.dx + b.dx) / 2, (a.dy + b.dy) / 2);
  final n = pts.length;
  final m = meio(pts[n - 1], pts[0]);
  final p = Path()..moveTo(m.dx, m.dy);
  for (var i = 0; i < n; i++) {
    final q = meio(pts[i], pts[(i + 1) % n]);
    p.quadraticBezierTo(pts[i].dx, pts[i].dy, q.dx, q.dy);
  }
  return p..close();
}

final _corpo = _suave(_superelipse(120, 112, 78, 60, 4));
final _tela = _suave(_superelipse(120, 109, 66, 45, 4.4));
final _silhueta = Path()
  ..addPath(_corpo, Offset.zero)
  ..addRRect(RRect.fromLTRBR(110, 164, 130, 190, const Radius.circular(4)))
  ..addRRect(RRect.fromLTRBR(84, 184, 156, 198, const Radius.circular(7)));

const _branco = [Color(0xFFFFFFFF), Color(0xFFEEF1F6), Color(0xFFB4BFD2)];
const _aros = Color(0xFF42537D);
const _corOlho = Color(0xFFEEF4FF);
const _rostoX = 120.0, _rostoY = 111.0, _rostoG = 25.0;

Color _misturar(Color a, Color b, double t) => Color.lerp(a, b, t)!;

/// Todo pintor do Otto desenha no enquadramento do SVG (8 16 224 224), do tamanho que vier.
void _enquadrar(Canvas c, Size s) {
  c.scale(s.width / 224, s.height / 224);
  c.translate(-8, -16);
}

Paint _desfoque(Color cor, double sigma) =>
    Paint()..color = cor..maskFilter = MaskFilter.blur(BlurStyle.normal, sigma);

// ---- camadas ----

class _Corpo extends CustomPainter {
  _Corpo(this.estado);
  final OttoEstado estado;

  @override
  void paint(Canvas c, Size s) {
    final st = _estilos[estado]!;
    final luz = st.cor ?? const Color(0xFF2C67FA);
    final brilhoCor = st.cor ?? const Color(0xFF5F8CFF);
    final forca = estado == OttoEstado.offline ? 0.08 : (st.cor != null ? 0.3 : 0.16);
    _enquadrar(c, s);

    // brilho em volta
    c.drawCircle(
      const Offset(120, 128),
      118,
      Paint()
        ..shader = RadialGradient(
          colors: [
            brilhoCor.withOpacity(forca),
            brilhoCor.withOpacity(_r1(forca * 3.8) / 10),
            brilhoCor.withOpacity(0),
          ],
          stops: const [0, 0.55, 1],
        ).createShader(Rect.fromCircle(center: const Offset(120, 128), radius: 118)),
    );

    // corpo de massinha: gradiente, tinta do estado, sombra de baixo e aro nas bordas
    final tudo = const Rect.fromLTWH(0, 0, 240, 240);
    c.save();
    c.clipPath(_silhueta);
    c.drawRect(
      tudo,
      Paint()
        ..shader = RadialGradient(colors: _branco, stops: const [0, 0.5, 1])
            .createShader(Rect.fromCircle(center: const Offset(98.2, 81.2), radius: 148.2)),
    );
    if (st.cor != null) {
      c.drawRect(
        tudo,
        Paint()
          ..shader = LinearGradient(
            begin: Alignment.topCenter,
            end: Alignment.bottomCenter,
            colors: [st.cor!.withOpacity(0), st.cor!.withOpacity(0.8)],
          ).createShader(const Rect.fromLTRB(0, 69.5, 240, 198)),
      );
    }
    c.drawRect(
      tudo,
      Paint()
        ..shader = LinearGradient(
          begin: Alignment.topCenter,
          end: Alignment.bottomCenter,
          colors: [const Color(0xFF060E22).withOpacity(0), const Color(0xFF060E22).withOpacity(0.28)],
        ).createShader(const Rect.fromLTRB(0, 125, 240, 198)),
    );
    // aro: a cor do aro some onde a silhueta desfocada é cheia, e fica só nas bordas
    c.saveLayer(tudo, Paint());
    c.drawRect(tudo, Paint()..color = _aros.withOpacity(0.45));
    c.drawPath(
      _silhueta,
      Paint()
        ..color = const Color(0xFF000000)
        ..blendMode = BlendMode.dstOut
        ..maskFilter = const MaskFilter.blur(BlurStyle.normal, 7),
    );
    c.restore();
    c.drawOval(
      Rect.fromCenter(center: const Offset(92, 59), width: 72, height: 14),
      _desfoque(Colors.white.withOpacity(0.78), 8),
    );
    c.save();
    c.translate(166, 59);
    c.rotate(6 * math.pi / 180);
    c.drawOval(Rect.fromCenter(center: Offset.zero, width: 24, height: 7), _desfoque(Colors.white.withOpacity(0.9), 3));
    c.restore();
    c.restore();

    // tela, reflexo e a luzinha de estado no pé
    final caixa = _tela.getBounds();
    c.drawPath(
      _tela,
      Paint()
        ..shader = const LinearGradient(
          begin: Alignment.topCenter,
          end: Alignment.bottomCenter,
          colors: [Color(0xFF12254F), Color(0xFF060D1C)],
        ).createShader(caixa),
    );
    c.save();
    c.clipPath(_tela);
    // brilho da luz vindo de baixo: no SVG é um radial em unidades da caixa da tela (elíptico)
    c.save();
    c.translate(caixa.left, caixa.top);
    c.scale(caixa.width, caixa.height);
    c.drawRect(
      const Rect.fromLTWH(0, 0, 1, 1),
      Paint()
        ..shader = RadialGradient(
          center: const Alignment(0, 1.1),
          radius: 0.9,
          colors: [luz.withOpacity(0.5), luz.withOpacity(0)],
        ).createShader(const Rect.fromLTWH(0, 0, 1, 1)),
    );
    c.restore();
    c.translate(86, 70);
    c.rotate(-24 * math.pi / 180);
    c.drawOval(
      Rect.fromCenter(center: Offset.zero, width: 104, height: 26),
      Paint()..color = Colors.white.withOpacity(0.07),
    );
    c.restore();
    c.drawPath(
      _tela,
      Paint()
        ..style = PaintingStyle.stroke
        ..strokeWidth = 1.5
        ..color = const Color(0xFF040914).withOpacity(0.55),
    );
    c.drawCircle(const Offset(120, 163), 6, _desfoque(luz.withOpacity(0.4), 3));
    c.drawCircle(const Offset(120, 163), 2.7, Paint()..color = luz);
  }

  @override
  bool shouldRepaint(_Corpo old) => old.estado != estado;
}

class _Bochechas extends CustomPainter {
  @override
  void paint(Canvas c, Size s) {
    _enquadrar(c, s);
    final p = _desfoque(const Color(0xFFFF6F91).withOpacity(0.5), 3);
    for (final x in [80.0, 160.0]) {
      c.drawOval(Rect.fromCenter(center: Offset(x, 122), width: 20, height: 12), p);
    }
  }

  @override
  bool shouldRepaint(_Bochechas old) => false;
}

class _OlhosPintor extends CustomPainter {
  _OlhosPintor(this.estado);
  final OttoEstado estado;

  void _olhos(Canvas c, _Olhos tipo, Paint traco, Paint cheio) {
    const y = _rostoY;
    for (final ex in [_rostoX - _rostoG, _rostoX + _rostoG]) {
      switch (tipo) {
        case _Olhos.feliz:
          c.drawPath(
            Path()
              ..moveTo(ex - 8, y + 3)
              ..quadraticBezierTo(ex, y - 10, ex + 8, y + 3),
            traco,
          );
        case _Olhos.traco:
          c.drawLine(Offset(ex - 7, y), Offset(ex + 7, y), traco);
        case _Olhos.dormindo:
          c.drawPath(
            Path()
              ..moveTo(ex - 8, y - 1)
              ..quadraticBezierTo(ex, y + 8, ex + 8, y - 1),
            traco,
          );
        case _Olhos.pontos:
        case _Olhos.olhando:
          final dx = tipo == _Olhos.olhando ? 4.0 : 0.0;
          final dy = tipo == _Olhos.olhando ? -1.0 : 0.0;
          c.drawOval(Rect.fromCenter(center: Offset(ex + dx, y + dy), width: 13.2, height: 17.6), cheio);
      }
    }
  }

  @override
  void paint(Canvas c, Size s) {
    final st = _estilos[estado]!;
    final luz = st.cor ?? const Color(0xFF2C67FA);
    _enquadrar(c, s);
    Paint traco(Color cor, [MaskFilter? f]) => Paint()
      ..style = PaintingStyle.stroke
      ..strokeWidth = 4.8
      ..strokeCap = StrokeCap.round
      ..strokeJoin = StrokeJoin.round
      ..color = cor
      ..maskFilter = f;
    Paint cheio(Color cor, [MaskFilter? f]) => Paint()
      ..color = cor
      ..maskFilter = f;
    // o brilho dos olhos na cor da luz (feDropShadow no SVG), depois os olhos
    const f = MaskFilter.blur(BlurStyle.normal, 3.2);
    _olhos(c, st.olhos, traco(luz.withOpacity(0.95), f), cheio(luz.withOpacity(0.95), f));
    _olhos(c, st.olhos, traco(_corOlho), cheio(_corOlho));
  }

  @override
  bool shouldRepaint(_OlhosPintor old) => old.estado != estado;
}

class _BalaoPintor extends CustomPainter {
  _BalaoPintor(this.estado);
  final OttoEstado estado;

  @override
  void paint(Canvas c, Size s) {
    final st = _estilos[estado]!;
    final cor = st.cor!;
    final tinta = st.tinta ?? Colors.white;
    _enquadrar(c, s);
    c.translate(200, 50);
    c.drawCircle(Offset.zero, 15, Paint()..color = cor);
    c.drawCircle(
      Offset.zero,
      15,
      Paint()
        ..style = PaintingStyle.stroke
        ..strokeWidth = 3.5
        ..color = const Color(0xFF060D18),
    );
    final linha = Paint()
      ..style = PaintingStyle.stroke
      ..strokeWidth = 3.4
      ..strokeCap = StrokeCap.round
      ..strokeJoin = StrokeJoin.round
      ..color = tinta;
    switch (st.balao) {
      case _Balao.cursor:
        c.drawPath(
          Path()
            ..moveTo(-4.6, -7.6)
            ..lineTo(-4.6, 6.2)
            ..lineTo(-1.2, 3)
            ..lineTo(1.2, 8.2)
            ..lineTo(3.9, 7)
            ..lineTo(1.6, 1.9)
            ..lineTo(6.4, 1.9)
            ..close(),
          Paint()..color = tinta,
        );
      case _Balao.pergunta:
        final tp = TextPainter(
          text: TextSpan(text: '?', style: TextStyle(fontSize: 17, fontWeight: FontWeight.w700, color: tinta, height: 1)),
          textDirection: TextDirection.ltr,
        )..layout();
        tp.paint(c, Offset(-tp.width / 2, -tp.height / 2));
      case _Balao.alerta:
        c.drawLine(const Offset(0, -7.5), const Offset(0, 1.5), linha);
        c.drawCircle(const Offset(0, 7), 2, Paint()..color = tinta);
      case _Balao.check:
        c.drawPath(
          Path()
            ..moveTo(-6.5, 0.5)
            ..lineTo(-2, 5)
            ..lineTo(6.5, -4.5),
          linha,
        );
      case _Balao.pontos:
      case null:
        break;
    }
  }

  @override
  bool shouldRepaint(_BalaoPintor old) => old.estado != estado;
}

/// Um pedaço pequeno do desenho (pontinho, mão, estrela, z), pintado no enquadramento todo.
class _Peca extends CustomPainter {
  _Peca(this.desenhar);
  final void Function(Canvas c) desenhar;

  @override
  void paint(Canvas c, Size s) {
    _enquadrar(c, s);
    desenhar(c);
  }

  @override
  bool shouldRepaint(_Peca old) => false;
}

class _Sombra extends CustomPainter {
  @override
  void paint(Canvas c, Size s) {
    // no chão, embaixo do pé: 50% da largura, de 84% a 91% da altura
    final r = Rect.fromLTWH(s.width * 0.25, s.height * 0.84, s.width * 0.5, s.height * 0.07);
    c.save();
    c.translate(r.left, r.top);
    c.scale(r.width, r.height);
    c.drawRect(
      const Rect.fromLTWH(0, 0, 1, 1),
      Paint()
        ..shader = RadialGradient(
          radius: 0.5,
          colors: [Colors.black.withOpacity(0.55), Colors.black.withOpacity(0)],
        ).createShader(const Rect.fromLTWH(0, 0, 1, 1)),
    );
    c.restore();
  }

  @override
  bool shouldRepaint(_Sombra old) => false;
}

// ---- tempo das animações (as mesmas do styles.css do painel) ----

double _entraSai(double p) => Curves.easeInOut.transform(p < 0.5 ? p * 2 : (1 - p) * 2);

/// Valor de uma animação de [periodo] segundos que roda [voltas] vezes depois de [atraso]; fora
/// dela, o repouso (0). [p] recebe a fração da volta e devolve o valor.
double _voltas(double t, double periodo, int voltas, double Function(double p) f, {double atraso = 0}) {
  final x = t - atraso;
  if (x < 0 || x >= periodo * voltas) return 0;
  return f((x % periodo) / periodo);
}

/// O Otto inteiro. A largura vem do [tamanho]; a altura é igual.
class Otto extends StatefulWidget {
  const Otto({super.key, this.estado = OttoEstado.parado, this.tamanho = 96, this.animado = true});

  final OttoEstado estado;
  final double tamanho;

  /// Flutuar, piscar e o resto. Desligue onde muitos Ottos aparecem juntos.
  final bool animado;

  @override
  State<Otto> createState() => _OttoState();
}

class _OttoState extends State<Otto> with TickerProviderStateMixin {
  Ticker? _relogio;
  final _tempo = ValueNotifier<double>(0);
  // os enfeites contam do momento em que o estado mudou (a mão acena de novo quando volta a "ola")
  double _inicioDoEstado = 0;

  bool get _vivo => widget.animado && !(MediaQuery.maybeOf(context)?.disableAnimations ?? false);

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _ajustarRelogio();
  }

  /// O relógio só roda com o Otto animado (e sem o "reduzir movimento" do sistema).
  void _ajustarRelogio() {
    if (_vivo && _relogio == null) {
      _relogio = createTicker((d) => _tempo.value = d.inMicroseconds / 1e6)..start();
    } else if (!_vivo && _relogio != null) {
      _relogio!.dispose();
      _relogio = null;
      _tempo.value = 0;
    }
  }

  @override
  void didUpdateWidget(Otto old) {
    super.didUpdateWidget(old);
    if (old.estado != widget.estado) _inicioDoEstado = _tempo.value;
    if (old.animado != widget.animado) _ajustarRelogio();
  }

  @override
  void dispose() {
    _relogio?.dispose();
    _tempo.dispose();
    super.dispose();
  }

  Widget _camada(CustomPainter p) => Positioned.fill(child: RepaintBoundary(child: CustomPaint(painter: p)));

  /// Uma peça que anima: [montar] recebe o tempo desde a mudança de estado e embrulha o desenho.
  Widget _animada(CustomPainter p, Widget Function(double t, Widget filho) montar) {
    final filho = RepaintBoundary(child: CustomPaint(painter: p));
    if (!_vivo) return Positioned.fill(child: filho);
    return Positioned.fill(
      child: ValueListenableBuilder<double>(
        valueListenable: _tempo,
        builder: (_, t, __) => montar(t - _inicioDoEstado, filho),
        child: filho,
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final e = widget.estado;
    final st = _estilos[e]!;
    final w = widget.tamanho;
    final pisca = st.olhos == _Olhos.pontos || st.olhos == _Olhos.olhando;
    final tinta = st.tinta ?? Colors.white;

    final desenho = Stack(children: [
      _camada(_Corpo(e)),
      _camada(_Bochechas()),
      // a piscada é da camada dos olhos inteira; o centro dos olhos fica a 42,4% da altura
      if (pisca)
        _animada(_OlhosPintor(e), (_, filho) {
          final p = (_tempo.value % 4.4) / 4.4;
          final y = p < 0.91
              ? 1.0
              : p < 0.95
                  ? 1 - 0.92 * Curves.ease.transform((p - 0.91) / 0.04)
                  : 0.08 + 0.92 * Curves.ease.transform((p - 0.95) / 0.05);
          return Transform(
            alignment: const Alignment(0, -0.152),
            transform: Matrix4.diagonal3Values(1, y, 1),
            child: filho,
          );
        })
      else
        _camada(_OlhosPintor(e)),
      if (st.balao != null && st.cor != null) _camada(_BalaoPintor(e)),
      if (st.balao == _Balao.pontos)
        for (final (i, dx) in [-6.5, 0.0, 6.5].indexed)
          _animada(
            _Peca((c) => c.drawCircle(Offset(200 + dx, 50), 2.5, Paint()..color = tinta)),
            (t, filho) {
              final o = _voltas(t, 1.2, 5, (p) {
                final k = p < 0.4 ? Curves.easeInOut.transform(p / 0.4) : 1 - Curves.easeInOut.transform((p - 0.4) / 0.6);
                return 1 - (0.35 + 0.65 * k);
              }, atraso: i * 0.18);
              return Opacity(opacity: 1 - o, child: filho);
            },
          ),
      if (e == OttoEstado.ola)
        _animada(
          _Peca((c) {
            const centro = Offset(214, 118);
            final r = Rect.fromCircle(center: centro, radius: 11.5);
            c.drawCircle(
              centro,
              11.5,
              Paint()
                ..shader = const RadialGradient(center: Alignment(-0.28, -0.44), radius: 0.85, colors: _branco, stops: [0, 0.55, 1])
                    .createShader(r),
            );
            c.drawCircle(
              centro,
              11.5,
              Paint()
                ..style = PaintingStyle.stroke
                ..strokeWidth = 1.2
                ..color = _aros.withOpacity(0.3),
            );
          }),
          (t, filho) => Transform.translate(
            offset: Offset(0, -0.0446 * w * _voltas(t, 1, 4, _entraSai)),
            child: filho,
          ),
        ),
      if (e == OttoEstado.concluido)
        for (final (i, (x, y, k)) in [(38.0, 44.0, 9.0), (58.0, 27.0, 6.0), (33.0, 19.0, 4.5)].indexed)
          _animada(
            _Peca((c) => c.drawPath(
                  Path()
                    ..moveTo(x, y - k)
                    ..quadraticBezierTo(x, y, x + k, y)
                    ..quadraticBezierTo(x, y, x, y + k)
                    ..quadraticBezierTo(x, y, x - k, y)
                    ..quadraticBezierTo(x, y, x, y - k)
                    ..close(),
                  Paint()..color = _misturar(st.cor!, Colors.white, 0.3),
                )),
            (t, filho) {
              final v = _voltas(t, 1.6, 3, _entraSai, atraso: i * 0.4);
              return Opacity(
                opacity: 1 - 0.5 * v,
                child: Transform.scale(
                  // cada estrela pulsa em volta do próprio centro
                  alignment: Alignment((x - 8) / 112 - 1, (y - 16) / 112 - 1),
                  scale: 1 - 0.45 * v,
                  child: filho,
                ),
              );
            },
          ),
      if (e == OttoEstado.offline)
        _animada(
          _Peca((c) {
            for (final (x, y, tam) in [(191.0, 59.0, 18.0), (206.0, 42.0, 13.0)]) {
              final tp = TextPainter(
                text: TextSpan(
                  text: 'z',
                  style: TextStyle(fontSize: tam, fontWeight: FontWeight.w500, fontFamily: 'monospace', color: const Color(0xFFA9B8D4)),
                ),
                textDirection: TextDirection.ltr,
              )..layout();
              tp.paint(c, Offset(x, y - tp.computeDistanceToActualBaseline(TextBaseline.alphabetic)));
            }
          }),
          (t, filho) {
            final v = _voltas(t, 2.6, 3, _entraSai);
            return Opacity(
              opacity: 0.9 - 0.4 * v,
              child: Transform.translate(offset: Offset(0, -0.0223 * w * v), child: filho),
            );
          },
        ),
    ]);

    final sombra = RepaintBoundary(child: CustomPaint(painter: _Sombra()));
    return SizedBox(
      width: w,
      height: w,
      child: !_vivo
          ? Stack(children: [Positioned.fill(child: sombra), Positioned.fill(child: desenho)])
          : ValueListenableBuilder<double>(
              valueListenable: _tempo,
              builder: (_, t, __) {
                // flutua e a sombra encolhe quando ele sobe (3,8 s, para sempre)
                final v = _entraSai((t % 3.8) / 3.8);
                return Stack(children: [
                  Positioned.fill(
                    child: Opacity(
                      opacity: 1 - 0.28 * v,
                      child: Transform.scale(scale: 1 - 0.12 * v, alignment: const Alignment(0, 0.75), child: sombra),
                    ),
                  ),
                  Positioned.fill(child: Transform.translate(offset: Offset(0, -0.026 * w * v), child: desenho)),
                ]);
              },
            ),
    );
  }
}

// ---- o Otto do status (ícone) ----

/// Status de um computador, para o Otto mini das listas.
enum OttoStatus { online, atendimento, semStatus, offline }

/// O Otto em tamanho de ícone (16 a 24 px), para o status nas listas: um desenho só, sem
/// desfoque (que nesse tamanho nem aparece), com o rosto ampliado para ler pequeno e o corpo
/// tingido na cor do status. Igual ao otto-mini.tsx do painel. Dá um pulinho quando o status
/// muda, nunca ao aparecer.
class OttoMini extends StatefulWidget {
  const OttoMini({super.key, required this.status, this.tamanho = 18});

  final OttoStatus status;
  final double tamanho;

  @override
  State<OttoMini> createState() => _OttoMiniState();
}

class _OttoMiniState extends State<OttoMini> with SingleTickerProviderStateMixin {
  late final _pulo = AnimationController(vsync: this, duration: const Duration(milliseconds: 600));

  @override
  void didUpdateWidget(OttoMini old) {
    super.didUpdateWidget(old);
    final semMovimento = MediaQuery.maybeOf(context)?.disableAnimations ?? false;
    if (old.status != widget.status && !semMovimento) _pulo.forward(from: 0);
  }

  @override
  void dispose() {
    _pulo.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final desenho = RepaintBoundary(
      child: CustomPaint(size: Size.square(widget.tamanho), painter: _MiniPintor(widget.status)),
    );
    return AnimatedBuilder(
      animation: _pulo,
      child: desenho,
      builder: (_, filho) {
        final p = _pulo.value;
        if (p == 0 || p == 1) return filho!;
        // sobe e cresce, quica um pouco abaixo do tamanho e assenta
        final (sobe, escala) = p < 0.35
            ? (Curves.easeOut.transform(p / 0.35), 1 + 0.25 * Curves.easeOut.transform(p / 0.35))
            : p < 0.7
                ? (1 - Curves.easeInOut.transform((p - 0.35) / 0.35), 1.25 - 0.31 * Curves.easeInOut.transform((p - 0.35) / 0.35))
                : (0.0, 0.94 + 0.06 * Curves.easeOut.transform((p - 0.7) / 0.3));
        return Transform.translate(
          offset: Offset(0, -0.22 * widget.tamanho * sobe),
          child: Transform.scale(scale: escala, child: filho),
        );
      },
    );
  }
}

final _corpoMini = _suave(_superelipse(120, 112, 78, 60, 4, pontos: 32));
final _telaMini = _suave(_superelipse(120, 109, 66, 45, 4.4, pontos: 32));

class _MiniPintor extends CustomPainter {
  _MiniPintor(this.status);
  final OttoStatus status;

  static const _cores = {
    OttoStatus.online: Color(0xFF31C48D),
    OttoStatus.atendimento: Color(0xFFF5B942),
    OttoStatus.semStatus: Color(0xFFD6A443),
    OttoStatus.offline: Color(0xFF7C8AA5),
  };

  @override
  void paint(Canvas c, Size s) {
    final cor = _cores[status]!;
    // enquadramento justo no monitor (38 46 164 158), para ele ler grande no ícone
    c.scale(s.width / 164, s.height / 158);
    c.translate(-38, -46);
    final pe = Paint()..color = _misturar(const Color(0xFFC9D1DF), cor, 0.4);
    c.drawRRect(RRect.fromLTRBR(110, 164, 130, 190, const Radius.circular(4)), pe);
    c.drawRRect(RRect.fromLTRBR(84, 184, 156, 198, const Radius.circular(7)), pe);
    c.drawPath(_corpoMini, Paint()..color = _misturar(const Color(0xFFEEF1F6), cor, 0.32));
    c.drawPath(
      _telaMini,
      Paint()
        ..color = status == OttoStatus.offline ? const Color(0xFF141B2B) : _misturar(const Color(0xFF0B1838), cor, 0.12),
    );
    const y = 108.0;
    final linha = Paint()
      ..style = PaintingStyle.stroke
      ..strokeWidth = 9
      ..strokeCap = StrokeCap.round
      ..color = _corOlho;
    for (final x in [95.0, 145.0]) {
      switch (status) {
        case OttoStatus.online:
          c.drawPath(Path()..moveTo(x - 12, y + 6)..quadraticBezierTo(x, y - 12, x + 12, y + 6), linha);
        case OttoStatus.offline:
          c.drawPath(Path()..moveTo(x - 12, y - 2)..quadraticBezierTo(x, y + 11, x + 12, y - 2), linha);
        case OttoStatus.atendimento:
        case OttoStatus.semStatus:
          final dx = status == OttoStatus.atendimento ? 7.0 : 0.0;
          c.drawOval(Rect.fromCenter(center: Offset(x + dx, y), width: 19, height: 25), Paint()..color = _corOlho);
      }
    }
  }

  @override
  bool shouldRepaint(_MiniPintor old) => old.status != status;
}
