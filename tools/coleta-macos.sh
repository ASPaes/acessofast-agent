#!/bin/sh
# AcessoFast — coleta de campo no macOS.
#
# Responde as perguntas que o plat_darwin.go ainda tem em aberto e que NAO dao pra
# deduzir do fonte do RustDesk: onde o cliente escreve o log de sessao, se os
# marcadores "#N Connection opened/closed" existem no macOS, como o launchd registra
# o servico, e que cara tem a saida do lsof e do ps NESTA maquina.
#
# Nao instala, nao configura, nao altera nada: so le e imprime.
#
# COMO USAR (Terminal do Mac):
#
#   sudo sh coleta-macos.sh antes     # antes de conectar
#   ...conecte no Mac a partir do Windows e deixe a sessao ABERTA...
#   sudo sh coleta-macos.sh durante   # com a sessao aberta
#   ...encerre a sessao...
#   sudo sh coleta-macos.sh depois    # logo apos encerrar
#
# O sudo e necessario: o servico roda como root, e sem sudo os caminhos de
# /var/root e os sockets do processo dele ficam invisiveis — o que daria a resposta
# ERRADA (pareceria que nao existe log nenhum).
#
# PRIVACIDADE: o script NAO imprime o conteudo dos arquivos de configuracao. Eles
# guardam o par de chaves e a senha permanente. Do .toml sai apenas a linha do "id".
# O trecho de log pode conter IP de quem conectou — se for incomodo, apague antes de
# mandar; os IPs nao fazem falta nenhuma pra analise.

FASE="${1:-antes}"

echo "================================================================"
echo " AcessoFast — coleta macOS   |   FASE: $FASE   |   $(date)"
echo "================================================================"

echo
echo "----- 1. Sistema -----------------------------------------------"
sw_vers
echo "arquitetura: $(uname -m)"

echo
echo "----- 2. O aplicativo ------------------------------------------"
# No teste voce usa o RustDesk OFICIAL. No cliente branded tudo isto vira
# "AcessoFast" no lugar de "RustDesk" — o que importa aqui e o FORMATO do caminho.
for app in /Applications/RustDesk.app /Applications/AcessoFast.app; do
	if [ -d "$app" ]; then
		echo "encontrado: $app"
		ls -l "$app/Contents/MacOS/"
	fi
done

echo
echo "----- 3. Como o launchd registra o servico ---------------------"
echo "# daemons/agents carregados:"
launchctl list 2>/dev/null | grep -i -E "rustdesk|carriez|acessofast" || echo "(nenhum no contexto do usuario)"
sudo launchctl list 2>/dev/null | grep -i -E "rustdesk|carriez|acessofast" || echo "(nenhum no contexto do sistema)"
echo "# arquivos .plist:"
ls -1 /Library/LaunchDaemons/ 2>/dev/null | grep -i -E "rustdesk|carriez|acessofast" || echo "(nenhum em /Library/LaunchDaemons)"
ls -1 /Library/LaunchAgents/ 2>/dev/null | grep -i -E "rustdesk|carriez|acessofast" || echo "(nenhum em /Library/LaunchAgents)"

echo
echo "----- 4. Processos e o formato do 'ps' -------------------------"
# CRITICO: o agente le o horario de inicio do processo com 'ps -o lstart='. Se este
# Mac responder com mes/dia em PORTUGUES, o parser precisa mudar — e so daqui da pra
# saber, porque depende do idioma da maquina.
# Com os ARGUMENTOS, e nao so o nome: no macOS o cliente se divide em varios
# processos (--server, --cm, a interface) e SO UM deles segura os sockets da sessao.
# Descobrir qual e o ponto desta secao — o agente precisa vigiar o certo.
ps -axo pid,user,lstart,args | grep -iE "[a]cessofast|[r]ustdesk" \
	|| echo "(nenhum processo do cliente rodando)"

APP=""
for cand in /Applications/AcessoFast.app /Applications/RustDesk.app; do
	[ -d "$cand" ] && APP="$cand" && break
done
EXEBASE="$(basename "${APP%.app}" 2>/dev/null)"
PIDS=""
[ -n "$APP" ] && PIDS=$(pgrep -f "$APP/Contents/MacOS/$EXEBASE" 2>/dev/null)
PID="$(echo "$PIDS" | head -1)"
echo "PIDs do cliente: $(echo $PIDS | tr '\n' ' ')"
if [ -n "$PID" ]; then
	# As duas leituras do horario: a do idioma da maquina e a que o agente usa.
	echo "ps -o lstart=          -> [$(ps -o lstart= -p "$PID")]"
	echo "ps -o lstart= (LC_ALL=C) -> [$(LC_ALL=C ps -o lstart= -p "$PID")]"
else
	echo "pgrep nao achou processo pelo caminho completo do executavel"
fi

echo
echo "----- 5. Config: onde fica e qual o ID -------------------------"
CONFS=$(sudo find /var/root/Library /Users/*/Library -maxdepth 7 \
	\( -iname "*.toml" \) 2>/dev/null | grep -i -E "rustdesk|carriez|acessofast")
if [ -z "$CONFS" ]; then
	echo "(nenhum .toml encontrado)"
else
	for f in $CONFS; do
		echo "arquivo: $f"
		# SO a linha do id. O resto do arquivo tem chave privada e senha.
		sudo grep -E "^[[:space:]]*id[[:space:]]*=" "$f" 2>/dev/null | sed 's/^/    /'
	done
fi

echo
echo "----- 6. LOGS: onde ficam  <<< A PERGUNTA MAIS IMPORTANTE ------"
LOGS=$(sudo find /var/root/Library/Logs /Users/*/Library/Logs /Library/Logs -maxdepth 6 \
	-iname "*.log" 2>/dev/null | grep -i -E "rustdesk|carriez|acessofast")
if [ -z "$LOGS" ]; then
	echo "(NENHUM log encontrado — isto seria a descoberta mais importante do teste)"
else
	for f in $LOGS; do
		sudo ls -l "$f"
	done
fi

echo
echo "----- 7. Marcadores de conexao dentro dos logs -----------------"
# E disto que a deteccao de sessao inteira depende. No Windows sao as linhas
# "#123 Connection opened" / "#123 Connection closed", que o motor so escreve no log
# do lado SERVIDOR (quem RECEBE a sessao).
if [ -z "$LOGS" ]; then
	echo "(sem logs pra procurar)"
else
	for f in $LOGS; do
		echo "=== $f"
		sudo grep -a -E "Connection opened|Connection closed|peer_id|connection count|new client|LoginRequest" "$f" 2>/dev/null \
			| tail -15 | sed 's/^/    /'
		echo "    (ultimas linhas do arquivo, pra ver o formato geral:)"
		sudo tail -5 "$f" 2>/dev/null | sed 's/^/    | /'
	done
fi

echo
echo "----- 8. Sockets do processo (prova de sessao viva) ------------"
# O agente usa isto pra detectar sessao fantasma: socket ESTABLISHED que nao seja o
# vinculo ocioso com o rendezvous significa que tem gente conectada.
# UM POR UM: na coleta anterior olhamos so o primeiro PID e o lsof veio vazio, o que
# nao prova ausencia de sessao — prova que o socket estava em OUTRO processo.
if [ -n "$PIDS" ]; then
	for p in $PIDS; do
		echo "### pid $p — $(ps -o args= -p "$p" 2>/dev/null | cut -c1-100)"
		echo "    -Fn (o formato que o parser do agente le):"
		sudo lsof -nP -a -p "$p" -iTCP -sTCP:ESTABLISHED -Fn 2>/dev/null | sed 's/^/      /'
		echo "    humano:"
		sudo lsof -nP -a -p "$p" -iTCP -sTCP:ESTABLISHED 2>/dev/null | sed 's/^/      /'
	done
else
	echo "(sem PID)"
fi

echo
echo "----- 9. O CLI do cliente responde? ----------------------------"
# O agente chama --get-id na matricula e --password na rotacao da senha.
EXE=""
[ -x /Applications/RustDesk.app/Contents/MacOS/RustDesk ] && EXE=/Applications/RustDesk.app/Contents/MacOS/RustDesk
[ -x /Applications/AcessoFast.app/Contents/MacOS/AcessoFast ] && EXE=/Applications/AcessoFast.app/Contents/MacOS/AcessoFast
if [ -n "$EXE" ]; then
	echo "--get-id -> [$("$EXE" --get-id 2>&1 | head -2)]"
	echo "(o LC_ALL=C mostra como o agente vai ler o ps:)"
	[ -n "$PID" ] && echo "ps com LC_ALL=C -> [$(LC_ALL=C ps -o lstart= -p "$PID")]"
else
	echo "(executavel nao encontrado)"
fi

echo
echo "================== FIM DA FASE: $FASE =========================="
