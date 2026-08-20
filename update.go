// AcessoFast — auto-update do agente (Passo 2 da atualizacao de frota).
//
// O canal ja existia: o agente posta 'presence' a cada 60s e le a resposta. Se a
// resposta trouxer um bloco "update", este arquivo baixa o binario novo, prova que
// ele e legitimo e troca o .exe em uso.
//
// O PULO DO GATO (Windows): um servico em execucao nao consegue SOBRESCREVER o
// proprio .exe, mas CONSEGUE renomea-lo. Renomeando o binario atual para .old, o
// caminho original fica livre pra receber o novo. O processo em execucao continua
// rodando a partir do arquivo renomeado ate o restart — nada quebra no meio.
//
// POR QUE ASSINATURA E NAO SO sha256: o hash chega pelo mesmo canal que a URL. Um
// servidor comprometido entregaria hash e binario coerentes entre si e o agente
// nao veria diferenca. Como este agente roda como SYSTEM em maquina de cliente,
// quem controla este caminho controla a frota inteira — e o alvo mais valioso do
// produto. A assinatura Ed25519 fecha isso: a chave privada vive so num GitHub
// Actions secret, entao comprometer o banco OU o Release nao basta pra forjar um
// update que este agente aceite.
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Chave PUBLICA que valida os manifestos de update. Publica por definicao — o que
// protege a frota e a privada, que nunca sai do GitHub Actions secret.
//
// Fica como constante do fonte (e nao injetada por ldflags) de proposito: um build
// sem a flag teria chave vazia e passaria a aceitar qualquer coisa que o servidor
// mandasse. Sendo constante, todo binario compilado deste fonte confere contra a
// mesma chave, sempre.
const updatePubKeyB64 = "IADLOND+FJeXkthXym/2AoPr6/336ITnC3TvOD1hGQs="

// updateDir: onde o binario baixado espera ate ser verificado e trocado. Derivado do
// baseDir, que e definido por plataforma (plat_windows.go / plat_darwin.go).
var updateDir = filepath.Join(baseDir, "update")

const (
	// Teto do download. O agente tem ~8 MB; 100 MB e folga larga que ainda impede
	// um servidor hostil de encher o disco da maquina do cliente.
	updateMaxBytes = 100 << 20
	// Tentativas por versao antes de desistir ate o proximo restart do servico.
	// Sem isto, um release com hash errado seria rebaixado a cada 60s, pra sempre.
	updateMaxTries = 3
)

// updateInfo e o bloco "update" da resposta da session-ingest.
type updateInfo struct {
	Version   string `json:"version"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

// Quantas vezes cada versao ja falhou nesta execucao. Estado so tocado pela
// goroutine do worker (o update roda no ramo de 'presence'), sem mutex.
var updateTries = map[string]int{}

// Versao ja trocada no disco nesta execucao, aguardando o restart agendado.
//
// Existe por causa de um comportamento visto em campo (2026-08-15, primeiro
// auto-update real): entre a troca e o restart ha ~2min em que o processo vivo
// ainda e o binario ANTIGO, entao o agente segue se declarando na versao velha.
// O servidor — corretamente — reoferece o update, e sem esta guarda o agente
// rebaixava os ~5,7 MB e falhava no rename com "Access is denied", porque o .old
// que ele tentaria sobrescrever e a imagem do proprio processo em execucao.
//
// Em memoria basta, e e o certo: o restart zera este estado, e depois dele a
// versao corrente JA e a nova, entao o servidor para de oferecer sozinho.
// Mesmo padrao sem mutex do updateTries — so a goroutine do worker toca.
var updateAplicado string

// manifestoCanonico e EXATAMENTE a string que o CI assina. Qualquer divergencia de
// formato aqui reprova todo update — de proposito: e melhor a frota parar de
// atualizar do que aceitar um manifesto que ninguem assinou.
//
// O prefixo com nome e versao do protocolo da separacao de dominio: impede que uma
// assinatura gerada pra outro proposito com a mesma chave seja reaproveitada aqui.
func manifestoCanonico(version, sha256hex string) string {
	return "acessofast-agent:v1:" + version + ":" + strings.ToLower(sha256hex)
}

// verificaAssinatura prova que o par (version, sha256) foi assinado por quem tem a
// chave privada. Note que NAO assinamos a URL: se a assinatura cobre o hash, mudar
// a URL nao serve de nada (o conteudo baixado nao casaria com o hash). Deixar a URL
// de fora e o que permite trocar de hospedagem sem reassinar releases antigos.
func verificaAssinatura(u *updateInfo) error {
	pub, err := base64.StdEncoding.DecodeString(updatePubKeyB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("chave publica embutida invalida")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(u.Signature))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("assinatura mal formada")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), []byte(manifestoCanonico(u.Version, u.SHA256)), sig) {
		return fmt.Errorf("assinatura NAO confere")
	}
	return nil
}

// baixaEConfere baixa a URL pro disco e devolve o caminho, tendo provado que o
// conteudo casa com o sha256 do manifesto ja verificado. O hash e calculado durante
// a escrita (streaming) — nao carregamos o binario inteiro em memoria.
func baixaEConfere(u *updateInfo) (string, error) {
	if err := os.MkdirAll(updateDir, 0755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", updateDir, err)
	}
	dest := filepath.Join(updateDir, "acessofast-agent-"+u.Version+exeSuffix)
	tmp := dest + ".part"
	_ = os.Remove(tmp)

	// Timeout proprio e generoso: e um binario de ~8 MB numa maquina de cliente que
	// pode estar em link ruim. O httpClient global (12s) e curto demais pra isso.
	cli := &http.Client{Timeout: 10 * time.Minute}
	resp, err := cli.Get(u.URL)
	if err != nil {
		return "", fmt.Errorf("GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET HTTP %d", resp.StatusCode)
	}

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return "", fmt.Errorf("criar %s: %w", tmp, err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, updateMaxBytes))
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("baixando: %w", err)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("fechando %s: %w", tmp, closeErr)
	}
	if n >= updateMaxBytes {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("download excedeu o teto de %d bytes", updateMaxBytes)
	}

	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, strings.TrimSpace(u.SHA256)) {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("sha256 nao confere (esperado %s, veio %s)", u.SHA256, got)
	}

	_ = os.Remove(dest)
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("renomear pra %s: %w", dest, err)
	}
	return dest, nil
}

// copia grava src em dst. Usado pra levar o binario ja verificado do updateDir
// (ProgramData) pra pasta do executavel (Program Files) ANTES da troca: o
// os.Rename da troca so e atomico dentro do mesmo volume, e nada garante que
// ProgramData e Program Files estejam no mesmo disco.
func copia(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}

// trocaBinario poe o novo binario no caminho do executavel atual.
//
// A ordem importa e cada passo tem desfazer:
//
//  1. copia o verificado pra .new, na MESMA pasta do .exe (mesmo volume)
//  2. renomeia o .exe atual  -> .old   (libera o caminho; o processo segue vivo)
//  3. renomeia o .new        -> .exe
//
// Se (3) falhar depois de (2), o caminho do servico esta VAZIO — a maquina nao
// subiria o agente no proximo boot e so uma sessao remota resolveria. Por isso (3)
// tem restauracao explicita do .old. E o unico ponto deste arquivo onde uma falha
// custaria a maquina.
func trocaBinario(verificado string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("os.Executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	novo := exe + ".new"
	velho := exe + ".old"

	if err := copia(verificado, novo); err != nil {
		return fmt.Errorf("copiar pra %s: %w", novo, err)
	}

	// Um .old de uma troca anterior ocuparia o nome e faria (2) falhar.
	_ = os.Remove(velho)
	if err := os.Rename(exe, velho); err != nil {
		_ = os.Remove(novo)
		return fmt.Errorf("renomear %s -> .old: %w", exe, err)
	}
	if err := os.Rename(novo, exe); err != nil {
		// Restaura: sem isto o servico fica sem binario.
		if rerr := os.Rename(velho, exe); rerr != nil {
			// Aqui a maquina precisa mesmo de intervencao — loga o caminho exato.
			logln("FATAL update: %s ficou sem binario e a restauracao falhou (%v). "+
				"Recuperar manualmente: renomear %s de volta pra %s", exe, rerr, velho, exe)
		}
		_ = os.Remove(novo)
		return fmt.Errorf("renomear .new -> %s: %w", exe, err)
	}
	return nil
}

// agendaRestart (por plataforma) faz o agente voltar a subir ja no binario novo.
// Falhar aqui NAO e grave e por isso o chamador so loga: o binario novo JA esta no
// caminho certo, entao o proximo boot da maquina (ou qualquer restart do servico)
// ja sobe na versao nova. O agendamento so antecipa isso.

// motivoPular decide se um manifesto deve ser IGNORADO e diz por que; "" significa
// "pode aplicar". E deliberadamente PURA — sem rede, disco nem estado global — para
// que a decisao seja testavel sozinha. O aplicaUpdate abaixo e que faz o IO, e
// testa-lo em unidade exigiria mocks que provariam menos do que estes casos.
//
// Os motivos NAO sao logados a cada presence de proposito (so o de build local):
// durante a janela ate o restart o servidor reoferece o mesmo update a cada 60s, e
// logar cada recusa encheria o agent.log de ruido.
func motivoPular(u *updateInfo, versaoAtual, jaAplicado string, tentativas int) string {
	switch {
	case u == nil || u.Version == "" || u.URL == "" || u.SHA256 == "" || u.Signature == "":
		return "manifesto incompleto"
	// Build local nao se auto-atualiza: em desenvolvimento a versao e "dev" e
	// qualquer alvo pareceria diferente, entao o agente trocaria por baixo de quem
	// esta depurando.
	case versaoAtual == "dev":
		return "build local (version=dev)"
	case u.Version == versaoAtual:
		return "ja estamos nesta versao" // o servidor nem deveria ter mandado
	case u.Version == jaAplicado:
		return "ja trocada no disco; aguardando o restart agendado"
	case tentativas >= updateMaxTries:
		return "falhou demais nesta execucao"
	}
	return ""
}

// aplicaUpdate roda o fluxo inteiro. Chamado SO no ramo de 'presence' do worker —
// ou seja, com a maquina comprovadamente ociosa. Nunca no meio de um atendimento.
func aplicaUpdate(u *updateInfo) {
	if u == nil {
		return
	}
	if motivo := motivoPular(u, version, updateAplicado, updateTries[u.Version]); motivo != "" {
		if version == "dev" {
			logln("update %s ignorado: %s", u.Version, motivo)
		}
		return
	}
	updateTries[u.Version]++

	// A ORDEM aqui e deliberada: assinatura ANTES do download. Verificar primeiro
	// evita gastar banda da maquina do cliente baixando algo que ja da pra provar
	// que nao e legitimo.
	if err := verificaAssinatura(u); err != nil {
		logln("update %s REPROVADO na assinatura: %v (nada foi baixado)", u.Version, err)
		return
	}
	logln("update %s: assinatura confere, baixando de %s", u.Version, u.URL)

	verificado, err := baixaEConfere(u)
	if err != nil {
		logln("update %s falhou no download/hash: %v (tentativa %d/%d)",
			u.Version, err, updateTries[u.Version], updateMaxTries)
		return
	}

	if err := trocaBinario(verificado); err != nil {
		logln("update %s falhou na troca do binario: %v", u.Version, err)
		return
	}
	updateAplicado = u.Version
	logln("update %s: binario trocado (anterior guardado em .old)", u.Version)

	if err := agendaRestart(); err != nil {
		// Nao e falha do update: o binario novo ja esta no lugar.
		logln("update %s: restart nao agendado (%v) — sobe no proximo boot do servico", u.Version, err)
		return
	}
	logln("update %s: restart agendado pra daqui a ~2min; a versao nova se declara no presence seguinte", u.Version)
}
