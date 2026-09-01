// AcessoFast — o contrato entre o agente e o instalador macOS.
//
// Sem build tag de proposito, como o saida_macos.go: assim o teste que confere este
// valor contra o .plist de verdade roda em qualquer maquina, inclusive no CI do
// Windows. Sob tag, so um Mac verificaria — e a divergencia passaria batida ate
// chegar num cliente.
package main

// rotuloDaemonAgente e o Label do LaunchDaemon do agente.
//
// Ele aparece em DOIS lugares que precisam concordar:
//
//	installer/macos/br.com.acessofast.agent.plist   (quem instala)
//	plat_darwin.go, agentLaunchdLabel               (quem reinicia)
//
// O agente usa este rotulo para se reiniciar depois que o auto-update troca o proprio
// binario. Se os dois divergirem, a troca acontece e o restart nao — a maquina
// continua rodando a versao velha, indefinidamente, sem erro em lugar nenhum. O
// painel mostraria a versao antiga e ninguem saberia por que.
//
// TestPlistDoAgenteUsaOMesmoRotulo compara este valor com o arquivo.
const rotuloDaemonAgente = "br.com.acessofast.agent"
