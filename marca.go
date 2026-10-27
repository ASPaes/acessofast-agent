// AcessoFast — marca.go — logo da empresa no app deste computador (27/10/2026).
//
// O administrador da empresa dona desta maquina sobe a logo no painel. No presence o servidor
// manda o bloco "marca" so quando ela muda: versao nova com uma URL assinada de vida curta, ou
// versao "" para tirar (a empresa removeu, ou o plano deixou de ter direito). O agente baixa,
// confere que e mesmo PNG ou JPG e grava em marcaDir; o app (inicio.dart) le de la e mostra
// "Seu suporte" junto do ID.
//
// Junto vai o acesso.json: de onde veio o acesso em curso ("painel", "externo" ou
// "desconhecido"), que vem do servidor em todo start/heartbeat. A janela de acesso do app
// (recebido.dart) usa para escolher entre o selo com a logo e o aviso de quem veio de fora.
//
// POR QUE ESTA PASTA. O app roda como o usuario logado e precisa LER; o resto de baseDir e
// trancado para SYSTEM + Administradores (o agent.token mora la). marcaDir ganha a mesma
// tranca, mais leitura para Users — ler e o suficiente, e so o servico grava. O usuario chega
// nela pelo caminho completo mesmo sem listar baseDir: o "bypass traverse checking" vem
// concedido a todo mundo no Windows.
//
// Tudo e best-effort: falhar aqui deixa a maquina com a logo que ja tinha (ou sem logo), e
// nunca atrapalha presenca, sessao ou cobranca.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var marcaDir = baseDir + `\marca`

const (
	marcaArquivo  = "marca.json"
	marcaLogo     = "logo"
	acessoArquivo = "acesso.json"
	// O mesmo limite do bucket logos-empresas. Conferido de novo aqui porque e este processo,
	// com SYSTEM, que grava o arquivo que o app vai abrir.
	marcaTamanhoMax = 1 << 20
	// So baixa do proprio storage do AcessoFast: a URL vem assinada pela session-ingest.
	marcaOrigemURL = "https://plmfyibyrowbgjjyblcl.supabase.co/storage/"
)

// marcaServidor e o bloco "marca" da resposta do presence.
type marcaServidor struct {
	Versao string `json:"versao"`
	Nome   string `json:"nome"`
	URL    string `json:"url"`
}

// marcaLocal e o marca.json que o app le.
type marcaLocal struct {
	Versao string `json:"versao"`
	Nome   string `json:"nome"`
}

var marcaPastaPronta sync.Once

// preparaPastaMarca cria marcaDir com a tranca do baseDir mais leitura para Users. SIDs, nao
// nomes, pelo mesmo motivo do hardenDir: em Windows pt-BR "Users" se chama "Usuarios".
func preparaPastaMarca() {
	marcaPastaPronta.Do(func() {
		if err := os.MkdirAll(marcaDir, 0o755); err != nil {
			logln("marca: nao criou %s: %v", marcaDir, err)
			return
		}
		cmd := exec.Command("icacls", marcaDir,
			"/inheritance:r",
			"/grant:r", "*S-1-5-18:(OI)(CI)F",
			"/grant:r", "*S-1-5-32-544:(OI)(CI)F",
			"/grant:r", "*S-1-5-32-545:(OI)(CI)RX",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			logln("marca: icacls falhou: %v — %s", err, strings.TrimSpace(string(out)))
		}
	})
}

// marcaVersaoAtual e a versao gravada nesta maquina, "" quando nao ha logo. Vai em todo
// presence: e com ela que o servidor decide se manda alguma coisa.
func marcaVersaoAtual() string {
	return versaoNaMarca(marcaDir)
}

func versaoNaMarca(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, marcaArquivo))
	if err != nil {
		return ""
	}
	var m marcaLocal
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	// Sem a imagem a versao nao vale: devolver "" faz o servidor mandar a logo de novo.
	if _, err := os.Stat(filepath.Join(dir, marcaLogo)); err != nil {
		return ""
	}
	return m.Versao
}

// aplicaMarca grava ou tira a logo conforme o bloco do servidor.
func aplicaMarca(m *marcaServidor) {
	if m == nil {
		return
	}
	preparaPastaMarca()
	if m.Versao == "" {
		if err := tiraMarca(marcaDir); err != nil {
			logln("marca: nao tirou a logo: %v", err)
			return
		}
		logln("marca: logo tirada")
		return
	}
	if !strings.HasPrefix(m.URL, marcaOrigemURL) {
		logln("marca: URL fora do storage do AcessoFast, ignorada")
		return
	}
	img, err := baixaLogo(m.URL)
	if err != nil {
		logln("marca: download falhou: %v", err)
		return
	}
	if err := gravaMarca(marcaDir, m.Versao, m.Nome, img); err != nil {
		logln("marca: nao gravou: %v", err)
		return
	}
	logln("marca: logo %s gravada (%d bytes)", m.Versao, len(img))
}

func baixaLogo(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// Le um byte alem do teto para saber se passou, em vez de gravar a imagem cortada.
	b, err := io.ReadAll(io.LimitReader(resp.Body, marcaTamanhoMax+1))
	if err != nil {
		return nil, err
	}
	if len(b) > marcaTamanhoMax {
		return nil, fmt.Errorf("imagem passa de %d bytes", marcaTamanhoMax)
	}
	return b, nil
}

// ehImagemDeLogo confere a assinatura do arquivo: o app so vai abrir PNG ou JPG, e e o
// conteudo que decide, nao o nome nem o content-type que veio na resposta.
func ehImagemDeLogo(b []byte) bool {
	return bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")) || bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF})
}

// nomeDaMarca limpa o nome que vai para a tela: sem quebra de linha e com tamanho de rotulo.
func nomeDaMarca(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if !utf8.ValidString(s) {
		return ""
	}
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80])
	}
	return s
}

// gravaMarca grava a imagem e depois o marca.json, cada um num arquivo temporario trocado
// de uma vez. O json vai por ultimo porque e ele que diz ao app que ha logo nova: o app
// nunca ve a versao nova apontando para a imagem velha pela metade.
func gravaMarca(dir, versao, nome string, img []byte) error {
	if !ehImagemDeLogo(img) {
		return fmt.Errorf("conteudo nao e PNG nem JPG")
	}
	if err := gravaTrocando(filepath.Join(dir, marcaLogo), img); err != nil {
		return err
	}
	b, _ := json.Marshal(marcaLocal{Versao: versao, Nome: nomeDaMarca(nome)})
	return gravaTrocando(filepath.Join(dir, marcaArquivo), b)
}

// tiraMarca apaga o json primeiro: sem ele o app ja para de mostrar, mesmo que a imagem
// fique para tras por algum motivo.
func tiraMarca(dir string) error {
	for _, nome := range []string{marcaArquivo, marcaLogo} {
		if err := os.Remove(filepath.Join(dir, nome)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func gravaTrocando(caminho string, conteudo []byte) error {
	tmp := caminho + ".tmp"
	if err := os.WriteFile(tmp, conteudo, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, caminho)
}

// ---------------------------------------------------------------------------
// De onde veio o acesso em curso
// ---------------------------------------------------------------------------

type acessoLocal struct {
	Origem string `json:"origem"`
	Em     string `json:"em"`
}

// gravaOrigemAcesso reescreve o acesso.json a cada start/heartbeat (20 s). O carimbo "em"
// deixa o app desconfiar de um arquivo velho: se o agente parou de bater, a origem gravada
// pode ser de outro acesso, e o app nao mostra nem logo nem aviso.
func gravaOrigemAcesso(origem string) {
	if origem != "painel" && origem != "externo" && origem != "desconhecido" {
		return
	}
	preparaPastaMarca()
	b, _ := json.Marshal(acessoLocal{Origem: origem, Em: time.Now().UTC().Format(time.RFC3339)})
	if err := gravaTrocando(filepath.Join(marcaDir, acessoArquivo), b); err != nil {
		logln("marca: nao gravou a origem do acesso: %v", err)
	}
}

// tiraOrigemAcesso roda no fim da sessao: sem acesso em curso, nao ha origem para mostrar.
func tiraOrigemAcesso() {
	if err := os.Remove(filepath.Join(marcaDir, acessoArquivo)); err != nil && !os.IsNotExist(err) {
		logln("marca: nao tirou a origem do acesso: %v", err)
	}
}
