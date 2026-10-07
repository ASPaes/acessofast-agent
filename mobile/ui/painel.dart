// CÓPIA de acessofast-desktop/ui/lib/painel.dart: mesmo código no app do celular. Mudou lá, copie para cá
// (e vice-versa).
//
// Conversa com o painel do AcessoFast (Supabase): login do técnico, computadores e marcadores
// da empresa, e a conexão "igual ao painel" (edge function connect-device).
//
// Usa as mesmas APIs e as mesmas regras de acesso (RLS) que o painel web: o técnico só enxerga
// a própria empresa. Nada aqui usa chave de serviço.
import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;
import 'package:flutter_hbb/models/platform_model.dart';

// Mesmo projeto e mesma chave pública do painel (app-acessofast/.env). A chave "anon" é pública
// por natureza: quem protege os dados são as regras de acesso do banco.
const _url = 'https://plmfyibyrowbgjjyblcl.supabase.co';
const _chave =
    'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJzdXBhYmFzZSIsInJlZiI6InBsbWZ5aWJ5cm93YmdqanlibGNsIiwicm9sZSI6ImFub24iLCJpYXQiOjE3ODM2NDMyNjIsImV4cCI6MjA5OTIxOTI2Mn0.grcQYqN3fHvFTWI0AFPWG66k1wONuGqZ5yMt07qcjxE';
const _opcaoSessao = 'acessofast-painel-sessao';

/// Erro já traduzido para mostrar ao técnico.
class ErroPainel implements Exception {
  final String mensagem;
  final String codigo;
  ErroPainel(this.mensagem, [this.codigo = '']);
  @override
  String toString() => mensagem;
}

class Perfil {
  final String id, nome, email, papel;
  final String? empresaId;
  final bool ativo;
  Perfil({required this.id, required this.nome, required this.email, required this.papel, required this.empresaId, required this.ativo});
  bool get admin => papel == 'admin' || papel == 'super_admin';
}

class ComputadorEmpresa {
  final String id; // address_book.id
  final String rustdeskId;
  final String nome;
  final String os;
  final String status; // online | offline | atendimento | sem_status | inativo
  final bool privado;
  final String? cliente;
  /// Grupo do painel (address_book.device_group): é por ele que o painel agrupa os computadores.
  final String? grupo;
  /// CNPJ ou CPF do cliente, só dígitos (clients.document). Entra na busca.
  final String? documento;
  final String empresaId; // tenant do computador: é nele que os marcadores dele moram
  final String empresaNome;
  final DateTime? ultimoSinal;
  ComputadorEmpresa({
    required this.id,
    required this.rustdeskId,
    required this.nome,
    required this.os,
    required this.status,
    required this.privado,
    required this.cliente,
    required this.grupo,
    this.documento,
    required this.empresaId,
    required this.empresaNome,
    required this.ultimoSinal,
  });
  bool get online => status == 'online' || status == 'atendimento';
}

/// Cliente da empresa (tabela clients do painel).
class ClienteEmpresa {
  final String id;
  final String nome;
  final String? documento;
  ClienteEmpresa({required this.id, required this.nome, this.documento});
}

class Marcador {
  final String id;
  String rotulo;
  String cor;
  final String empresaId;
  Marcador({required this.id, required this.rotulo, required this.cor, required this.empresaId});
}

/// Resposta do connect-device.
class Acesso {
  final String rustdeskId;
  final String? senha;
  final bool privado;
  Acesso(this.rustdeskId, this.senha, this.privado);
}

/// Saldo da empresa, igual ao quadro do topo da tela Dispositivos do painel.
class Carteira {
  final String modo; // free | credits | plan
  final int creditos;
  final int gratisRestantes;
  final int gratisLimite;
  final int sessoesAtivas;
  Carteira({required this.modo, required this.creditos, required this.gratisRestantes, required this.gratisLimite, required this.sessoesAtivas});
  /// Conta que paga por atendimento (gratuita ou por créditos). Com plano, o quadro some.
  bool get medida => modo == 'free' || modo == 'credits';
}

/// Pacote de créditos à venda (tabela credit_packages, a mesma do Financeiro do painel).
class PacoteCreditos {
  final String codigo;
  final int creditos;
  final int precoCentavos;
  PacoteCreditos(this.codigo, this.creditos, this.precoCentavos);
}

/// Tela Financeiro do painel: onde fica a loja completa, as faturas e o histórico.
const urlFinanceiro = 'https://app.acessofast.com.br/financeiro';

/// connect-device pediu para escolher entre acesso gratuito e crédito.
class EscolhaCobranca implements Exception {
  final int gratisRestantes;
  final int creditos;
  EscolhaCobranca(this.gratisRestantes, this.creditos);
}

class Painel extends ChangeNotifier {
  Painel._();
  static final instancia = Painel._();

  String? _token, _renovacao;
  DateTime _expira = DateTime.fromMillisecondsSinceEpoch(0);
  Perfil? perfil;

  List<ComputadorEmpresa> computadores = [];
  List<Marcador> marcadores = [];
  // device_id -> ids dos marcadores
  Map<String, Set<String>> marcacoes = {};
  Carteira? carteira;
  bool carregando = false;

  bool get logado => perfil != null;

  // ------------------------------------------------------------------ sessão

  Future<void> restaurar() async {
    final salvo = bind.mainGetLocalOption(key: _opcaoSessao);
    if (salvo.isEmpty) return;
    try {
      final m = jsonDecode(salvo) as Map<String, dynamic>;
      _renovacao = m['r'] as String?;
      if (_renovacao == null) return;
      await _renovar();
      await _carregarPerfil();
      await atualizar();
    } catch (_) {
      // Sessão vencida ou revogada: volta para o estado sem login, sem alarde.
      await _limpar();
    }
  }

  Future<void> entrar(String email, String senha) async {
    final r = await http.post(
      Uri.parse('$_url/auth/v1/token?grant_type=password'),
      headers: {'apikey': _chave, 'Content-Type': 'application/json'},
      body: jsonEncode({'email': email.trim(), 'password': senha}),
    );
    if (r.statusCode != 200) {
      final corpo = _json(r.body);
      final msg = '${corpo['error_description'] ?? corpo['msg'] ?? corpo['error'] ?? ''}';
      if (msg.contains('Invalid login')) throw ErroPainel('E-mail ou senha incorretos.');
      if (msg.contains('not confirmed')) throw ErroPainel('Confirme o e-mail pelo convite antes de entrar.');
      throw ErroPainel('Não foi possível entrar agora. Confira a internet e tente de novo.');
    }
    _guardarTokens(_json(r.body));
    await _carregarPerfil();
    final p = perfil!;
    if (!p.ativo) {
      await sair();
      throw ErroPainel('Seu usuário está desativado. Fale com o administrador da sua empresa.');
    }
    if (p.empresaId == null && p.papel != 'super_admin') {
      await sair();
      throw ErroPainel('Sua conta ainda está aguardando a liberação da empresa.');
    }
    await atualizar();
  }

  Future<void> sair() async {
    final t = _token;
    await _limpar();
    if (t != null) {
      unawaited(http.post(Uri.parse('$_url/auth/v1/logout'), headers: {'apikey': _chave, 'Authorization': 'Bearer $t'}).catchError((_) => http.Response('', 0)));
    }
  }

  Future<void> _limpar() async {
    _token = _renovacao = null;
    perfil = null;
    computadores = [];
    marcadores = [];
    marcacoes = {};
    carteira = null;
    await bind.mainSetLocalOption(key: _opcaoSessao, value: '');
    notifyListeners();
  }

  void _guardarTokens(Map<String, dynamic> m) {
    _token = m['access_token'] as String?;
    _renovacao = m['refresh_token'] as String?;
    final seg = (m['expires_in'] as num?)?.toInt() ?? 3600;
    _expira = DateTime.now().add(Duration(seconds: seg - 60));
    // Só o token de renovação fica salvo: o de acesso vale 1 hora e é refeito ao abrir o app.
    bind.mainSetLocalOption(key: _opcaoSessao, value: jsonEncode({'r': _renovacao}));
  }

  Future<void> _renovar() async {
    final r = await http.post(
      Uri.parse('$_url/auth/v1/token?grant_type=refresh_token'),
      headers: {'apikey': _chave, 'Content-Type': 'application/json'},
      body: jsonEncode({'refresh_token': _renovacao}),
    );
    if (r.statusCode != 200) throw ErroPainel('Sessão expirada. Entre de novo.', 'sessao');
    _guardarTokens(_json(r.body));
  }

  Future<Map<String, String>> _cabecalhos() async {
    if (_token == null || DateTime.now().isAfter(_expira)) await _renovar();
    return {'apikey': _chave, 'Authorization': 'Bearer $_token', 'Content-Type': 'application/json'};
  }

  // ------------------------------------------------------------------ REST

  Future<dynamic> _get(String caminho) async {
    final r = await http.get(Uri.parse('$_url/rest/v1/$caminho'), headers: await _cabecalhos());
    if (r.statusCode == 401) throw ErroPainel('Sessão expirada. Entre de novo.', 'sessao');
    if (r.statusCode >= 300) throw ErroPainel('O painel não respondeu. Tente de novo em instantes.');
    return jsonDecode(r.body);
  }

  Future<dynamic> _enviar(String metodo, String caminho, [Object? corpo, String duplicado = 'Já existe um marcador com esse nome.']) async {
    final req = http.Request(metodo, Uri.parse('$_url/rest/v1/$caminho'))
      ..headers.addAll(await _cabecalhos())
      ..headers['Prefer'] = 'return=representation';
    if (corpo != null) req.body = jsonEncode(corpo);
    final r = await http.Response.fromStream(await req.send());
    if (r.statusCode >= 300) {
      final m = _json(r.body);
      if ('${m['code']}' == '23505') throw ErroPainel(duplicado, '23505');
      debugPrint('painel $metodo $caminho -> ${r.statusCode} ${r.body}');
      throw ErroPainel('O painel recusou a alteração. Tente de novo.');
    }
    return r.body.isEmpty ? null : jsonDecode(r.body);
  }

  static Map<String, dynamic> _json(String s) {
    try {
      final v = jsonDecode(s);
      return v is Map<String, dynamic> ? v : {};
    } catch (_) {
      return {};
    }
  }

  Future<void> _carregarPerfil() async {
    final uid = _uidDoToken();
    final l = await _get('profiles?select=id,role,tenant_id,full_name,email,is_active&id=eq.$uid') as List;
    if (l.isEmpty) throw ErroPainel('Perfil não encontrado no painel.');
    final m = l.first as Map<String, dynamic>;
    perfil = Perfil(
      id: m['id'],
      nome: (m['full_name'] as String?)?.trim().isNotEmpty == true ? m['full_name'] : (m['email'] ?? ''),
      email: m['email'] ?? '',
      papel: m['role'] ?? 'tech',
      empresaId: m['tenant_id'],
      ativo: m['is_active'] != false,
    );
  }

  String _uidDoToken() {
    final partes = (_token ?? '').split('.');
    if (partes.length < 2) throw ErroPainel('Sessão inválida.');
    final carga = utf8.decode(base64Url.decode(base64Url.normalize(partes[1])));
    return (jsonDecode(carga) as Map<String, dynamic>)['sub'] as String;
  }

  // ------------------------------------------------------------------ dados

  /// Recarrega computadores, marcadores e marcações da empresa.
  Future<void> atualizar() async {
    if (!logado) return;
    carregando = true;
    notifyListeners();
    try {
      final devs = await _get(
          'v_dispositivo_status?select=id,tenant_id,empresa_nome,rustdesk_id,alias,os,status_presenca,privado,cliente_nome,cliente_documento,device_group,last_online&is_active=eq.true&order=alias.asc.nullslast') as List;
      computadores = [
        for (final d in devs.cast<Map<String, dynamic>>())
          ComputadorEmpresa(
            id: d['id'],
            rustdeskId: '${d['rustdesk_id'] ?? ''}',
            nome: (d['alias'] as String?)?.trim().isNotEmpty == true ? d['alias'] : '${d['rustdesk_id']}',
            os: d['os'] ?? '',
            status: d['status_presenca'] ?? 'sem_status',
            privado: d['privado'] == true,
            cliente: d['cliente_nome'],
            grupo: d['device_group'],
            documento: d['cliente_documento'],
            empresaId: d['tenant_id'],
            empresaNome: d['empresa_nome'] ?? '',
            ultimoSinal: DateTime.tryParse('${d['last_online'] ?? ''}')?.toLocal(),
          ),
      ];
      final ms = await _get('device_markers?select=id,tenant_id,label,color&order=label.asc') as List;
      marcadores = [for (final m in ms.cast<Map<String, dynamic>>()) Marcador(id: m['id'], rotulo: m['label'], cor: m['color'] ?? 'slate', empresaId: m['tenant_id'])];
      final as = await _get('device_marker_assignments?select=device_id,marker_id') as List;
      marcacoes = {};
      for (final a in as.cast<Map<String, dynamic>>()) {
        marcacoes.putIfAbsent(a['device_id'] as String, () => <String>{}).add(a['marker_id'] as String);
      }
      await atualizarCarteira();
    } on ErroPainel catch (e) {
      if (e.codigo == 'sessao') await _limpar();
      rethrow;
    } finally {
      carregando = false;
      notifyListeners();
    }
  }

  /// Mesmas consultas do painel (dispositivos.tsx, "billing_carteira" e "atendimentos_ativos").
  /// O administrador da plataforma não tem empresa: para ele não há carteira.
  Future<void> atualizarCarteira() async {
    final empresa = perfil?.empresaId;
    if (empresa == null) return;
    try {
      // O dia do limite grátis é o de São Paulo (UTC-3, sem horário de verão desde 2019).
      final sp = DateTime.now().toUtc().subtract(const Duration(hours: 3));
      final hoje = '${sp.year}-${sp.month.toString().padLeft(2, '0')}-${sp.day.toString().padLeft(2, '0')}';
      final agora = Uri.encodeQueryComponent(DateTime.now().toUtc().toIso8601String());
      final r = await Future.wait([
        _get('tenants?select=billing_mode&id=eq.$empresa'),
        _get('credit_ledger?select=credits&tenant_id=eq.$empresa'),
        _get('daily_access?select=used,cap&tenant_id=eq.$empresa&access_date=eq.$hoje'),
        _get('atendimentos?select=address_book_id&ended_at=is.null&window_expires_at=gt.$agora'),
      ]);
      final tenant = (r[0] as List).cast<Map<String, dynamic>>();
      final creditos = (r[1] as List).cast<Map<String, dynamic>>().fold<int>(0, (s, m) => s + ((m['credits'] as num?)?.toInt() ?? 0));
      final dia = (r[2] as List).cast<Map<String, dynamic>>();
      final cap = dia.isEmpty ? 5 : ((dia.first['cap'] as num?)?.toInt() ?? 5);
      final usados = dia.isEmpty ? 0 : ((dia.first['used'] as num?)?.toInt() ?? 0);
      final ativos = (r[3] as List).cast<Map<String, dynamic>>().map((m) => m['address_book_id']).whereType<String>().toSet().length;
      carteira = Carteira(
        modo: tenant.isEmpty ? 'plan' : '${tenant.first['billing_mode'] ?? 'plan'}',
        creditos: creditos,
        gratisRestantes: (cap - usados).clamp(0, cap),
        gratisLimite: cap,
        sessoesAtivas: ativos,
      );
      notifyListeners();
    } on ErroPainel {
      // Sem a carteira o resto do app funciona; o quadro só não aparece.
    }
  }

  Future<List<PacoteCreditos>> pacotesDeCreditos() async {
    final l = await _get('credit_packages?select=code,credits,price_cents&is_active=eq.true&order=sort_order.asc') as List;
    return [
      for (final m in l.cast<Map<String, dynamic>>())
        PacoteCreditos('${m['code']}', (m['credits'] as num?)?.toInt() ?? 0, (m['price_cents'] as num?)?.toInt() ?? 0),
    ];
  }

  /// Link de pagamento do pacote: o mesmo create-credit-checkout que o botão Comprar do painel usa.
  Future<String> linkDeCompra(String codigoPacote) async {
    final r = await http.post(
      Uri.parse('$_url/functions/v1/create-credit-checkout'),
      headers: await _cabecalhos(),
      body: jsonEncode({'package_code': codigoPacote}),
    );
    final m = _json(r.body);
    final url = m['checkout_url'];
    if (r.statusCode >= 300 || url is! String || url.isEmpty) {
      debugPrint('create-credit-checkout: ${r.statusCode} ${r.body}');
      throw ErroPainel('Não foi possível abrir o pagamento agora. Tente de novo ou compre pelo Financeiro do painel.');
    }
    return url;
  }

  int usoDoMarcador(String id) => marcacoes.values.where((s) => s.contains(id)).length;

  /// Empresas que o técnico enxerga (id -> nome). Técnico e admin: só a dele. Administrador da
  /// plataforma: todas as que têm computador.
  Map<String, String> get empresas {
    final m = <String, String>{};
    for (final d in computadores) {
      m.putIfAbsent(d.empresaId, () => d.empresaNome.isEmpty ? 'Empresa sem nome' : d.empresaNome);
    }
    return m;
  }

  Future<void> alternarMarcacao(ComputadorEmpresa d, String markerId) async {
    final deviceId = d.id;
    final atual = marcacoes[deviceId] ?? <String>{};
    if (atual.contains(markerId)) {
      await _enviar('DELETE', 'device_marker_assignments?device_id=eq.$deviceId&marker_id=eq.$markerId');
      atual.remove(markerId);
    } else {
      await _enviar('POST', 'device_marker_assignments', {'tenant_id': d.empresaId, 'device_id': deviceId, 'marker_id': markerId});
      atual.add(markerId);
    }
    marcacoes[deviceId] = atual;
    notifyListeners();
  }

  /// [empresaId]: o marcador pertence a uma empresa. Técnico e admin só têm a própria; o
  /// administrador da plataforma não tem empresa e cria na empresa do computador (ou na escolhida).
  Future<Marcador> criarMarcador(String rotulo, String cor, String empresaId) async {
    final r = await _enviar('POST', 'device_markers', {'tenant_id': empresaId, 'label': rotulo.trim(), 'color': cor}) as List;
    final m = r.first as Map<String, dynamic>;
    final novo = Marcador(id: m['id'], rotulo: m['label'], cor: m['color'], empresaId: m['tenant_id']);
    marcadores = [...marcadores, novo]..sort((a, b) => a.rotulo.toLowerCase().compareTo(b.rotulo.toLowerCase()));
    notifyListeners();
    return novo;
  }

  Future<void> editarMarcador(Marcador m, {String? rotulo, String? cor}) async {
    await _enviar('PATCH', 'device_markers?id=eq.${m.id}', {if (rotulo != null) 'label': rotulo.trim(), if (cor != null) 'color': cor});
    if (rotulo != null) m.rotulo = rotulo.trim();
    if (cor != null) m.cor = cor;
    notifyListeners();
  }

  Future<void> excluirMarcador(Marcador m) async {
    // As marcações somem junto (on delete cascade em device_marker_assignments).
    await _enviar('DELETE', 'device_markers?id=eq.${m.id}');
    marcadores = marcadores.where((x) => x.id != m.id).toList();
    for (final s in marcacoes.values) {
      s.remove(m.id);
    }
    notifyListeners();
  }

  // ------------------------------------------------------------------ adicionar computador

  Future<List<ClienteEmpresa>> clientes(String empresaId) async {
    final l = await _get('clients?select=id,name,document&tenant_id=eq.$empresaId&order=name.asc') as List;
    return [for (final m in l.cast<Map<String, dynamic>>()) ClienteEmpresa(id: m['id'], nome: m['name'], documento: m['document'])];
  }

  /// Cria o cliente; se já existe um com o mesmo nome, usa o existente (igual ao painel).
  Future<ClienteEmpresa> criarCliente(String empresaId, String nome) async {
    try {
      final r = await _enviar('POST', 'clients?select=id,name,document', {'tenant_id': empresaId, 'name': nome.trim()}, 'Já existe um cliente com esse nome.') as List;
      final m = r.first as Map<String, dynamic>;
      return ClienteEmpresa(id: m['id'], nome: m['name'], documento: m['document']);
    } on ErroPainel catch (e) {
      if (e.codigo != '23505') rethrow;
      final l = await _get('clients?select=id,name,document&tenant_id=eq.$empresaId&name=ilike.${Uri.encodeQueryComponent(nome.trim())}') as List;
      if (l.isEmpty) rethrow;
      final m = l.first as Map<String, dynamic>;
      return ClienteEmpresa(id: m['id'], nome: m['name'], documento: m['document']);
    }
  }

  /// Mesmo caminho do "Adicionar dispositivo" do painel: o adopt-device cadastra o computador
  /// (que precisa estar com o AcessoFast instalado e ligado) e, se ele é novo, grava cliente,
  /// observações e marcadores. Devolve true quando o computador entrou agora no painel e false
  /// quando ele já estava cadastrado.
  Future<bool> adicionarComputador({
    required String rustdeskId,
    String? nome,
    String? empresaId,
    ClienteEmpresa? cliente,
    String? observacoes,
    Set<String> marcadores = const {},
  }) async {
    final id = rustdeskId.replaceAll(RegExp(r'\D'), '');
    if (id.length < 6 || id.length > 12) throw ErroPainel('Digite o ID do computador (de 6 a 12 números).');
    final r = await http.post(
      Uri.parse('$_url/functions/v1/adopt-device'),
      headers: await _cabecalhos(),
      body: jsonEncode({
        'rustdesk_id': id,
        'alias': (nome ?? '').trim().isEmpty ? null : nome!.trim(),
        if (perfil?.papel == 'super_admin') 'tenant_id': empresaId,
      }),
    );
    final m = _json(r.body);
    final erro = '${m['error'] ?? (r.statusCode >= 300 ? r.body : '')}';
    if (erro.isNotEmpty && erro != 'null') throw ErroPainel(_traduzirErroAdocao(erro));
    final deviceId = m['device_id'] as String?;
    final novo = m['was_inserted'] == true;
    final empresa = empresaId ?? perfil?.empresaId;
    var incompleto = false;
    if (novo && deviceId != null) {
      // Cliente e observações num update só, como o painel.
      final patch = <String, dynamic>{
        if (cliente != null) 'client_id': cliente.id,
        if (cliente != null) 'device_group': cliente.nome,
        if ((observacoes ?? '').trim().isNotEmpty) 'observacoes': observacoes!.trim(),
      };
      try {
        if (patch.isNotEmpty) await _enviar('PATCH', 'address_book?id=eq.$deviceId', patch);
        if (marcadores.isNotEmpty && empresa != null) {
          await _enviar('POST', 'device_marker_assignments', [
            for (final mk in marcadores) {'tenant_id': empresa, 'device_id': deviceId, 'marker_id': mk},
          ]);
        }
      } on ErroPainel {
        incompleto = true;
      }
    }
    await atualizar().catchError((_) {});
    if (incompleto) {
      throw ErroPainel('O computador entrou no painel, mas o cliente, as observações ou os marcadores não foram gravados. Ajuste pelo painel.', 'incompleto');
    }
    return novo;
  }

  // Mesmos casos do "Adicionar dispositivo" do painel (src/routes/_authenticated/dispositivos.tsx).
  static String _traduzirErroAdocao(String e) {
    if (e.contains('no_pending_claim')) {
      return 'Esse computador ainda não apareceu no painel. Confirme que o AcessoFast está instalado nele e que ele está ligado e com internet, depois tente de novo.';
    }
    if (e.contains('rustdesk_id_invalido')) return 'ID inválido: digite de 6 a 12 números.';
    if (e.contains('tenant_id_obrigatorio')) return 'Escolha a empresa do computador.';
    if (e.contains('user_inactive')) return 'Seu usuário está desativado. Fale com o administrador da sua empresa.';
    if (e.contains('sem_tenant')) return 'Sua conta ainda não está ligada a uma empresa.';
    debugPrint('adopt-device: $e');
    return 'Não foi possível adicionar o computador agora. Tente de novo.';
  }

  // ------------------------------------------------------------------ conectar

  /// Igual ao botão Conectar do painel: registra o técnico, respeita horário e computador
  /// privado, e conta como atendimento. [origem] é 'free' ou 'credit' depois da escolha.
  Future<Acesso> pedirAcesso(String deviceId, {String? origem}) async {
    final r = await http.post(
      Uri.parse('$_url/functions/v1/connect-device'),
      headers: await _cabecalhos(),
      body: jsonEncode({'device_id': deviceId, if (origem != null) 'source': origem}),
    );
    final m = _json(r.body);
    final erro = '${m['error'] ?? (r.statusCode >= 300 ? r.body : '')}';
    if (erro.isNotEmpty && erro != 'null') throw ErroPainel(_traduzirErro(erro));
    if (m['needs_choice'] == true) {
      throw EscolhaCobranca((m['free_remaining'] as num?)?.toInt() ?? 0, (m['credit_balance'] as num?)?.toInt() ?? 0);
    }
    final rid = '${m['rustdesk_id'] ?? ''}';
    if (rid.isEmpty) throw ErroPainel('Resposta inválida do painel.');
    // O atendimento gastou um acesso grátis ou um crédito: o quadro do saldo muda.
    unawaited(atualizarCarteira());
    return Acesso(rid, m['password'] as String?, m['privado'] == true);
  }

  // Mesmos textos da tela de conectar do painel (src/routes/conectar.tsx).
  static String _traduzirErro(String e) {
    if (e.contains('sem_senha_provisionada')) return 'Este computador ainda não tem senha no painel. Provisione a senha antes de conectar.';
    if (e.contains('aguardando_agente')) return 'O computador ainda não confirmou a senha nova. Tente de novo em instantes.';
    if (e.contains('quota_exceeded')) return 'Limite de sessões simultâneas do plano atingido. Encerre uma sessão para conectar em outro computador.';
    if (e.contains('no_credits')) return 'Sem acessos gratuitos e sem créditos disponíveis. Compre créditos ou conheça os planos no painel.';
    if (e.contains('billing_blocked')) return 'Conta bloqueada por pendência de pagamento. Regularize na aba Financeiro do painel.';
    if (e.contains('conta_inativa')) return 'Empresa inativa. Fale com o suporte para reativar a conta.';
    if (e.contains('user_inactive')) return 'Seu usuário está desativado. Fale com o administrador da sua empresa.';
    if (e.contains('fora_do_horario')) return 'Fora do horário de acesso definido pela sua empresa.';
    if (e.contains('free_requires_individual')) return 'O acesso gratuito só vale para uma conexão por vez. Use um crédito para conexões simultâneas.';
    if (e.contains('device_inativo')) return 'Este computador está inativo no painel.';
    debugPrint('connect-device: $e');
    return 'Não foi possível conectar pelo painel agora. Tente de novo.';
  }
}
