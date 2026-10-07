// CÓPIA de acessofast-desktop/ui/lib/textos.dart: mesmo código no app do celular. Mudou lá, copie para cá
// (e vice-versa).
//
// Textos do AcessoFast por cima da tradução do RustDesk.
//
// Os avisos e menus que ainda são do RustDesk passam pelo translate() dele (common.dart), que
// pergunta aqui primeiro (ver patches/ em preparar.sh). Só entra o que está errado ou
// estranho no pt-BR do RustDesk ("Prompt" virava "Prompt de comando" no título de avisos);
// o resto continua com a tradução dele.
//
// Sem imports de propósito: o common.dart do RustDesk importa este arquivo.

/// Texto do AcessoFast para a chave do RustDesk, ou null para usar a tradução dele.
String? textoAcessoFast(String chave) => _textos[chave];

const _textos = <String, String>{
  // Títulos de aviso
  'Prompt': 'Aviso',
  'Note': 'Observação',
  'Successful': 'Pronto',
  'Connection Error': 'Não foi possível conectar',

  // Conexão
  'Connecting...': 'Conectando…',
  'Connection in progress. Please wait.': 'Conectando. Aguarde um instante.',
  'Logging in...': 'Entrando…',
  'Connected, waiting for image...': 'Conectado. Carregando a tela…',
  'Please wait for the remote side to accept your session request...':
      'Aguardando a pessoa do outro lado aceitar o acesso…',
  'Reset by the peer': 'O outro computador encerrou a conexão',
  'ID does not exist': 'Esse ID não existe. Confira os números e tente de novo.',
  'Remote desktop is offline': 'O computador está desligado ou sem internet',
  'Failed to connect via relay server': 'Não foi possível conectar pelo servidor do AcessoFast',
  'Failed to connect to relay server': 'Não foi possível falar com o servidor do AcessoFast',
  'Key mismatch': 'A chave de segurança do servidor não confere',
  'Do you want to enter again?': 'Quer tentar de novo?',
  'Please enter your password': 'Digite a senha do computador',
  'Wrong Password': 'Senha incorreta',
  'Remember password': 'Lembrar a senha neste computador',
  'Retry': 'Tentar de novo',

  // Login do Windows
  'OS Password': 'Senha do Windows',
  'OS Account': 'Usuário do Windows',

  // Recursos da sessão
  'Privacy mode': 'Tela escura no outro computador',
  'Elevate': 'Pedir acesso de administrador',
  'Use all my displays for the remote session': 'Dividir os monitores sozinho ao conectar',
  'Show displays as individual windows': 'Cada monitor numa janela separada',
  'Insert Lock': 'Bloquear o computador',
  'Restart remote device': 'Reiniciar o computador',
  'Lock after session end': 'Bloquear o computador ao terminar',
  'Show quality monitor': 'Mostrar a qualidade da conexão',
  'Show remote cursor': 'Mostrar o cursor do outro computador',

  // Menus da sessão (Ações, Exibição, Teclado: common/widgets/toolbar.dart do RustDesk)
  'Insert Ctrl + Alt + Del': 'Enviar Ctrl + Alt + Del',
  'Request Elevation': 'Pedir acesso de administrador',
  'Switch Sides': 'Inverter: o outro lado acessa você',
  'TCP tunneling': 'Túnel de rede (TCP)',
  'Transfer file': 'Transferir arquivos',
  'View camera': 'Ver a câmera',
  'Virtual display': 'Monitor virtual',
  'Plug out all': 'Remover todos os monitores virtuais',
  'Refresh': 'Atualizar a imagem',
  'Reset canvas': 'Centralizar a imagem',
  'Take screenshot': 'Capturar a tela',
  'Taking screenshot': 'Capturando a tela…',
  'Copy Fingerprint': 'Copiar o código de segurança da conexão',
  'Scale original': 'Tamanho original',
  'Scale adaptive': 'Ajustar à janela',
  'Scale custom': 'Tamanho personalizado',
  'Good image quality': 'Melhor imagem',
  'Balanced': 'Equilibrada',
  'Optimize reaction time': 'Mais rápida',
  'Custom': 'Personalizada',
  'True color (4:4:4)': 'Cores fiéis (4:4:4)',
  'Follow remote cursor': 'Acompanhar o cursor do outro computador',
  'Follow remote window focus': 'Acompanhar a janela ativa do outro computador',
  'Zoom cursor': 'Cursor ampliado',
  'Mute': 'Sem som',
  'Disable clipboard': 'Desligar copiar e colar',
  'Enable file copy and paste': 'Copiar e colar arquivos',
  'View Mode': 'Só ver, sem controlar',
  'Relative mouse mode': 'Mouse relativo (jogos e programas 3D)',
  'Reverse mouse wheel': 'Inverter a rolagem do mouse',
  'swap-left-right-mouse': 'Trocar os botões do mouse',
  'Swap control-command key': 'Trocar Ctrl e Command',
  'Send clipboard keystrokes': 'Digitar o texto copiado',
  'Why this happens': 'Por que isso acontece?',

  // Arquivos
  'This file exists, skip or overwrite this file?': 'Já existe um arquivo com esse nome. Pular ou substituir?',
  'Do this for all conflicts': 'Fazer o mesmo para os outros arquivos repetidos',
  'Are you sure you want to delete this file?': 'Apagar este arquivo? Não dá para desfazer.',
  'Confirm Delete': 'Apagar arquivo',
};
