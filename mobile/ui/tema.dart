// CÓPIA de acessofast-desktop/ui/lib/tema.dart: o app do celular usa as mesmas cores e fontes do
// desktop. Mudou lá, copie para cá (e vice-versa).
//
// Tema do AcessoFast: as mesmas cores e fontes do protótipo aprovado (prototipo/app.css),
// que por sua vez são as do painel web.
import 'package:flutter/material.dart';

const kFonte = 'PlusJakartaSans';
const kFonteMono = 'JetBrainsMonoAF';

@immutable
class AfCores extends ThemeExtension<AfCores> {
  final Color bg, side, surface, surface2, raised, hover, line, line2;
  final Color primary, primaryHover, accentText, cyan;
  final Color text, text2, muted;
  final Color ok, warn, danger, dangerText;
  final Color idA, idB, idC, glow;

  const AfCores({
    required this.bg,
    required this.side,
    required this.surface,
    required this.surface2,
    required this.raised,
    required this.hover,
    required this.line,
    required this.line2,
    required this.primary,
    required this.primaryHover,
    required this.accentText,
    required this.cyan,
    required this.text,
    required this.text2,
    required this.muted,
    required this.ok,
    required this.warn,
    required this.danger,
    required this.dangerText,
    required this.idA,
    required this.idB,
    required this.idC,
    required this.glow,
  });

  static const escuro = AfCores(
    bg: Color(0xFF060D18),
    side: Color(0xFF070F1C),
    surface: Color(0xFF101E37),
    surface2: Color(0xFF142543),
    raised: Color(0xFF192D52),
    hover: Color(0xFF1C3157),
    line: Color(0xFF22304A),
    line2: Color(0xFF2C3D5C),
    primary: Color(0xFF2C67FA),
    primaryHover: Color(0xFF1D54E0),
    accentText: Color(0xFF64A4FF),
    cyan: Color(0xFF22D3EE),
    text: Color(0xFFF7F9FC),
    text2: Color(0xFFB2BED0),
    muted: Color(0xFF909DB3),
    ok: Color(0xFF31C48D),
    warn: Color(0xFFF5B942),
    danger: Color(0xFFF05252),
    dangerText: Color(0xFFFF8A8A),
    idA: Color(0xFF7FD8FF),
    idB: Color(0xFF64A4FF),
    idC: Color(0xFF8FB2FF),
    glow: Color(0x292C67FA),
  );

  static const claro = AfCores(
    bg: Color(0xFFF4F7FC),
    side: Color(0xFFFFFFFF),
    surface: Color(0xFFFFFFFF),
    surface2: Color(0xFFEEF3FB),
    raised: Color(0xFFE2EAF7),
    hover: Color(0xFFE8EEF9),
    line: Color(0xFFDBE3EF),
    line2: Color(0xFFC5D1E3),
    primary: Color(0xFF2C67FA),
    primaryHover: Color(0xFF1D54E0),
    accentText: Color(0xFF1D54E0),
    cyan: Color(0xFF0E7490),
    text: Color(0xFF0D1A30),
    text2: Color(0xFF33425C),
    muted: Color(0xFF5D6B82),
    ok: Color(0xFF0C8A5F),
    warn: Color(0xFFA8660B),
    danger: Color(0xFFDC3B3B),
    dangerText: Color(0xFFC42F2F),
    idA: Color(0xFF0B7FB3),
    idB: Color(0xFF1D54E0),
    idC: Color(0xFF3A4FD6),
    glow: Color(0x142C67FA),
  );

  static AfCores of(BuildContext context) =>
      Theme.of(context).extension<AfCores>() ??
      (Theme.of(context).brightness == Brightness.dark ? escuro : claro);

  @override
  AfCores copyWith() => this;

  @override
  AfCores lerp(ThemeExtension<AfCores>? other, double t) =>
      (other is AfCores && t >= 0.5) ? other : this;
}

/// Aplica as cores e a fonte do AcessoFast sobre o tema do RustDesk, mantendo as extensões
/// dele (as telas antigas que ainda não trocamos continuam funcionando).
ThemeData temaAcessoFast(ThemeData base, {required bool escuro}) {
  final c = escuro ? AfCores.escuro : AfCores.claro;
  final esquema = base.colorScheme.copyWith(
    primary: c.primary,
    onPrimary: Colors.white,
    secondary: c.accentText,
    surface: c.surface,
    onSurface: c.text,
    error: c.danger,
  );
  return base.copyWith(
    colorScheme: esquema,
    scaffoldBackgroundColor: c.bg,
    canvasColor: c.bg,
    cardColor: c.surface,
    dividerColor: c.line,
    hoverColor: c.hover,
    textTheme: base.textTheme.apply(fontFamily: kFonte, bodyColor: c.text, displayColor: c.text),
    primaryTextTheme: base.primaryTextTheme.apply(fontFamily: kFonte),
    tooltipTheme: TooltipThemeData(
      decoration: BoxDecoration(color: c.text, borderRadius: BorderRadius.circular(6)),
      textStyle: TextStyle(fontFamily: kFonte, fontSize: 12, color: c.bg),
    ),
    // Os avisos e diálogos que continuam sendo do RustDesk ("Conectando…", senha, erro de
    // conexão, substituir/apagar arquivo) usam os componentes padrão: com o tema eles ficam
    // com a cara dos nossos (_Dialogo em inicio.dart).
    dialogTheme: DialogTheme(
      backgroundColor: c.surface,
      surfaceTintColor: Colors.transparent,
      elevation: 12,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14), side: BorderSide(color: c.line2)),
      titleTextStyle: TextStyle(fontFamily: kFonte, fontSize: 17, fontWeight: FontWeight.w700, color: c.text),
      contentTextStyle: TextStyle(fontFamily: kFonte, fontSize: 13.5, height: 1.4, color: c.text2),
    ),
    elevatedButtonTheme: ElevatedButtonThemeData(
      style: ButtonStyle(
        elevation: const WidgetStatePropertyAll(0),
        // O tema do RustDesk usa densidade compacta: sem isto os botões encolhem para 30 px.
        visualDensity: VisualDensity.standard,
        minimumSize: const WidgetStatePropertyAll(Size(88, 38)),
        padding: const WidgetStatePropertyAll(EdgeInsets.symmetric(horizontal: 16)),
        shape: WidgetStatePropertyAll(RoundedRectangleBorder(borderRadius: BorderRadius.circular(9))),
        textStyle: const WidgetStatePropertyAll(TextStyle(fontFamily: kFonte, fontSize: 13, fontWeight: FontWeight.w700)),
        backgroundColor: WidgetStateProperty.resolveWith((s) => s.contains(WidgetState.disabled)
            ? c.line2
            : s.contains(WidgetState.hovered)
                ? c.primaryHover
                : c.primary),
        foregroundColor: WidgetStateProperty.resolveWith((s) => s.contains(WidgetState.disabled) ? c.muted : Colors.white),
        overlayColor: const WidgetStatePropertyAll(Colors.transparent),
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: ButtonStyle(
        visualDensity: VisualDensity.standard,
        minimumSize: const WidgetStatePropertyAll(Size(88, 38)),
        padding: const WidgetStatePropertyAll(EdgeInsets.symmetric(horizontal: 16)),
        shape: WidgetStatePropertyAll(RoundedRectangleBorder(borderRadius: BorderRadius.circular(9))),
        side: WidgetStatePropertyAll(BorderSide(color: c.line2)),
        textStyle: const WidgetStatePropertyAll(TextStyle(fontFamily: kFonte, fontSize: 13, fontWeight: FontWeight.w700)),
        foregroundColor: WidgetStatePropertyAll(c.text),
        backgroundColor: WidgetStateProperty.resolveWith((s) => s.contains(WidgetState.hovered) ? c.hover : c.surface),
        overlayColor: const WidgetStatePropertyAll(Colors.transparent),
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: ButtonStyle(
        textStyle: const WidgetStatePropertyAll(TextStyle(fontFamily: kFonte, fontSize: 13, fontWeight: FontWeight.w600)),
        foregroundColor: WidgetStatePropertyAll(c.accentText),
        shape: WidgetStatePropertyAll(RoundedRectangleBorder(borderRadius: BorderRadius.circular(8))),
      ),
    ),
    progressIndicatorTheme: ProgressIndicatorThemeData(color: c.primary, linearTrackColor: c.line, circularTrackColor: c.line),
    checkboxTheme: CheckboxThemeData(
      fillColor: WidgetStateProperty.resolveWith((s) => s.contains(WidgetState.selected) ? c.primary : Colors.transparent),
      checkColor: const WidgetStatePropertyAll(Colors.white),
      side: BorderSide(color: c.line2, width: 1.5),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(5)),
    ),
    radioTheme: RadioThemeData(fillColor: WidgetStateProperty.resolveWith((s) => s.contains(WidgetState.selected) ? c.primary : c.line2)),
    switchTheme: SwitchThemeData(
      thumbColor: const WidgetStatePropertyAll(Colors.white),
      trackColor: WidgetStateProperty.resolveWith((s) => s.contains(WidgetState.selected) ? c.primary : c.line2),
      trackOutlineColor: const WidgetStatePropertyAll(Colors.transparent),
    ),
    // Campos dos diálogos do RustDesk (senha, usuário do Windows). Os nossos campos desligam
    // borda e fundo por conta própria, então isto não muda nada neles.
    inputDecorationTheme: InputDecorationTheme(
      // Sem contentPadding/isDense aqui: o isCollapsed dos nossos campos herdaria o espaçamento.
      filled: true,
      fillColor: c.bg,
      hintStyle: TextStyle(fontFamily: kFonte, fontSize: 14, color: c.muted),
      labelStyle: TextStyle(fontFamily: kFonte, fontSize: 14, color: c.text2),
      border: OutlineInputBorder(borderRadius: BorderRadius.circular(9), borderSide: BorderSide(color: c.line2, width: 1.5)),
      enabledBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(9), borderSide: BorderSide(color: c.line2, width: 1.5)),
      focusedBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(9), borderSide: BorderSide(color: c.primary, width: 1.5)),
      errorBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(9), borderSide: BorderSide(color: c.danger, width: 1.5)),
      focusedErrorBorder: OutlineInputBorder(borderRadius: BorderRadius.circular(9), borderSide: BorderSide(color: c.danger, width: 1.5)),
    ),
    popupMenuTheme: PopupMenuThemeData(
      color: c.surface,
      surfaceTintColor: Colors.transparent,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12), side: BorderSide(color: c.line2)),
      textStyle: TextStyle(fontFamily: kFonte, fontSize: 13, color: c.text2),
    ),
    extensions: [...base.extensions.values.where((e) => e is! AfCores), c],
  );
}
