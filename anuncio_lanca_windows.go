//go:build windows

// AcessoFast — anuncio_lanca_windows.go — lancar a janela do anuncio na sessao
// interativa, a partir do servico (sessao 0).
//
// O servico roda como SYSTEM em sessao 0 e nao desenha. Para a janela aparecer na
// tela de quem esta usando a maquina, pegamos o token do usuario logado
// (WTSQueryUserToken — so SYSTEM consegue), duplicamos para token primario e
// re-executamos ESTE MESMO binario com `mostrar-anuncio <payload>` via
// CreateProcessAsUser, no desktop interativo (winsta0\default).
//
// A imagem e BAIXADA aqui (o servico tem rede) para uma pasta que o usuario
// consegue ler (C:\Users\Public), e o caminho local vai no payload. A janela
// (anuncio_ui_windows.go) so le do disco.
//
// Tudo best-effort: qualquer falha devolve false, e o chamador (aviso.go) cai no
// WTSSendMessage de texto. Anuncio nunca pode quebrar nada.
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	dllWtsapi  = windows.NewLazySystemDLL("wtsapi32.dll")
	dllAdvapi  = windows.NewLazySystemDLL("advapi32.dll")
	dllUserenv = windows.NewLazySystemDLL("userenv.dll")

	procWTSQueryUserToken = dllWtsapi.NewProc("WTSQueryUserToken")
	procDuplicateTokenEx  = dllAdvapi.NewProc("DuplicateTokenEx")
	procCreateEnvBlock    = dllUserenv.NewProc("CreateEnvironmentBlock")
	procDestroyEnvBlock   = dllUserenv.NewProc("DestroyEnvironmentBlock")
)

const (
	_TOKEN_ALL_ACCESS           = 0x000F01FF
	_SecurityImpersonation      = 2
	_TokenPrimary               = 1
	_CREATE_UNICODE_ENVIRONMENT = 0x00000400
	publicDir                   = `C:\Users\Public`
	anuncioPrefixo              = "acessofast-anuncio-"
)

// lancaAnuncioNaSessao tenta desenhar a janela do anuncio na sessao do usuario.
// Devolve true se conseguiu iniciar o processo da janela; false se algo falhou
// (e ai o chamador mostra o texto por WTSSendMessage).
func lancaAnuncioNaSessao(a *avisoServidor) bool {
	if a == nil || strings.TrimSpace(a.ImageURL) == "" {
		return false
	}

	sessao := windows.WTSGetActiveConsoleSessionId()
	if sessao == 0xFFFFFFFF {
		// Ninguem no console (bloqueado / sem sessao). Sem imagem agora; o aviso
		// volta no proximo presence.
		logln("anuncio: nenhuma sessao interativa")
		return false
	}

	varreSobrasAnuncio() // limpa payload/imagem de execucoes anteriores

	ts := strconv.FormatInt(time.Now().UnixNano(), 10)
	imgPath := filepath.Join(publicDir, anuncioPrefixo+ts+".img")
	jsonPath := filepath.Join(publicDir, anuncioPrefixo+ts+".json")

	if err := baixaImagem(a.ImageURL, imgPath); err != nil {
		logln("anuncio: download da imagem falhou: %v", err)
		return false
	}

	payload := anuncioPayload{Titulo: a.Titulo, CtaURL: extraiCTA(a.Mensagem), ImageFile: imgPath}
	buf, err := json.Marshal(payload)
	if err != nil {
		os.Remove(imgPath)
		return false
	}
	if err := os.WriteFile(jsonPath, buf, 0644); err != nil {
		os.Remove(imgPath)
		return false
	}

	// Token do usuario logado -> token primario para CreateProcessAsUser.
	var userTok windows.Token
	if r, _, e := procWTSQueryUserToken.Call(uintptr(sessao), uintptr(unsafe.Pointer(&userTok))); r == 0 {
		logln("anuncio: WTSQueryUserToken falhou: %v", e)
		os.Remove(imgPath)
		os.Remove(jsonPath)
		return false
	}
	defer userTok.Close()

	var primTok windows.Token
	if r, _, e := procDuplicateTokenEx.Call(
		uintptr(userTok), _TOKEN_ALL_ACCESS, 0,
		_SecurityImpersonation, _TokenPrimary,
		uintptr(unsafe.Pointer(&primTok)),
	); r == 0 {
		logln("anuncio: DuplicateTokenEx falhou: %v", e)
		os.Remove(imgPath)
		os.Remove(jsonPath)
		return false
	}
	defer primTok.Close()

	// Bloco de ambiente do usuario (PATH, TEMP etc.) — sem ele o processo herda um
	// ambiente pobre. Best-effort: se falhar, segue com env nil.
	var env *uint16
	if r, _, _ := procCreateEnvBlock.Call(uintptr(unsafe.Pointer(&env)), uintptr(primTok), 0); r == 0 {
		env = nil
	}
	if env != nil {
		defer procDestroyEnvBlock.Call(uintptr(unsafe.Pointer(env)))
	}

	exe, err := os.Executable()
	if err != nil {
		os.Remove(imgPath)
		os.Remove(jsonPath)
		return false
	}
	cmdline := syscall.EscapeArg(exe) + " mostrar-anuncio " + syscall.EscapeArg(jsonPath)
	cmdPtr, _ := syscall.UTF16PtrFromString(cmdline)
	desktop, _ := syscall.UTF16PtrFromString(`winsta0\default`)

	var si windows.StartupInfo
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Desktop = desktop
	var pi windows.ProcessInformation

	flags := uint32(0)
	if env != nil {
		flags |= _CREATE_UNICODE_ENVIRONMENT
	}

	if err := windows.CreateProcessAsUser(
		primTok, nil, cmdPtr, nil, nil, false,
		flags, env, nil, &si, &pi,
	); err != nil {
		logln("anuncio: CreateProcessAsUser falhou: %v", err)
		os.Remove(imgPath)
		os.Remove(jsonPath)
		return false
	}
	windows.CloseHandle(pi.Thread)
	windows.CloseHandle(pi.Process)
	logln("anuncio: janela lancada na sessao %d", sessao)
	return true
}

// baixaImagem GET simples com timeout; grava o corpo no arquivo.
func baixaImagem(url, destino string) error {
	cli := &http.Client{Timeout: 15 * time.Second}
	resp, err := cli.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return &httpErro{resp.StatusCode}
	}
	f, err := os.Create(destino)
	if err != nil {
		return err
	}
	defer f.Close()
	// Teto de 8 MB: a arte e um cartaz, nao um video; poe um limite pra nao
	// escrever um corpo enorme por engano.
	_, err = io.Copy(f, io.LimitReader(resp.Body, 8<<20))
	return err
}

type httpErro struct{ code int }

func (e *httpErro) Error() string { return "http " + strconv.Itoa(e.code) }

// extraiCTA tira a URL do fim da mensagem que registrar_anuncio_esgotado monta
// (corpo + "\n\n" + cta_label + ":\n" + cta_url). Como a arte ja tem o botao
// desenhado, so precisamos da URL para o clique. Pega a ultima linha que parece
// URL; se nao achar, devolve "" (clique nao abre nada, janela so fecha).
func extraiCTA(mensagem string) string {
	linhas := strings.Split(mensagem, "\n")
	for i := len(linhas) - 1; i >= 0; i-- {
		l := strings.TrimSpace(linhas[i])
		if strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") {
			return l
		}
	}
	return ""
}

// varreSobrasAnuncio remove payloads/imagens de anuncios antigos (>10 min) que a
// janela nao tenha limpado (ex.: processo morto). Best-effort e silencioso.
func varreSobrasAnuncio() {
	ents, err := os.ReadDir(publicDir)
	if err != nil {
		return
	}
	limite := time.Now().Add(-10 * time.Minute)
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), anuncioPrefixo) {
			continue
		}
		info, err := e.Info()
		if err == nil && info.ModTime().Before(limite) {
			os.Remove(filepath.Join(publicDir, e.Name()))
		}
	}
}
