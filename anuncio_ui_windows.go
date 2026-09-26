//go:build windows

// AcessoFast — anuncio_ui_windows.go — a JANELA do anuncio, na sessao do usuario.
//
// PARA QUE SERVE
// O momento "esgotado" do acesso direto (.exe) mostrava so uma caixa nativa do
// Windows (aviso.go, WTSSendMessage) — que le como ERRO do sistema, nao como
// mensagem do app. Aqui desenhamos uma janela PROPRIA do AcessoFast: com marca
// (titulo + icone), a arte do anuncio, e clique que abre o navegador no CTA.
//
// POR QUE ESTE CODIGO RODA NO MESMO BINARIO
// O servico roda em sessao 0 (sem interface). Ele nao pode desenhar. Entao ele
// re-executa ESTE binario com o subcomando `mostrar-anuncio <payload.json>`
// DENTRO da sessao interativa do usuario (CreateProcessAsUser, ver
// anuncio_lanca_windows.go). Assim nao ha um segundo .exe pra empacotar no
// instalador — o mesmo binario ja instalado se re-executa.
//
// POR QUE A ARTE E SO UMA IMAGEM
// O criativo do AcessoFast ja traz headline, corpo e o botao "Conhecer os
// creditos" DESENHADOS na propria imagem. Entao a janela nao renderiza texto:
// ela exibe a imagem e o clique nela abre o CTA. Simples e robusto — sem toolkit
// grafico, so Win32 + GDI+ (que existe em toda edicao do Windows).
//
// COMPORTAMENTO (decidido em 26/09/2026): janela com MARCA, FECHAVEL, NAO em
// tela cheia. X e Esc fecham a qualquer momento. Sem travar o fechamento — o
// oposto de adware. Centralizada, fundo escuro solido, imagem proporcional.
package main

import (
	"encoding/json"
	"os"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Payload que o servico escreve e este processo le. A imagem ja vem BAIXADA para
// um arquivo local (o servico tem rede; ver anuncio_lanca_windows.go) — a janela
// so carrega do disco, nao fala com a rede.
type anuncioPayload struct {
	Titulo    string `json:"titulo"`     // vai na barra de titulo da janela
	CtaURL    string `json:"cta_url"`    // aberto no navegador ao clicar na arte
	ImageFile string `json:"image_file"` // caminho local do PNG/JPG baixado
}

var (
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modGdi32    = windows.NewLazySystemDLL("gdi32.dll")
	modGdiplus  = windows.NewLazySystemDLL("gdiplus.dll")
	modShell32  = windows.NewLazySystemDLL("shell32.dll")
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pRegisterClassExW = modUser32.NewProc("RegisterClassExW")
	pCreateWindowExW  = modUser32.NewProc("CreateWindowExW")
	pDefWindowProcW   = modUser32.NewProc("DefWindowProcW")
	pShowWindow       = modUser32.NewProc("ShowWindow")
	pUpdateWindow     = modUser32.NewProc("UpdateWindow")
	pGetMessageW      = modUser32.NewProc("GetMessageW")
	pTranslateMessage = modUser32.NewProc("TranslateMessage")
	pDispatchMessageW = modUser32.NewProc("DispatchMessageW")
	pPostQuitMessage  = modUser32.NewProc("PostQuitMessage")
	pDestroyWindow    = modUser32.NewProc("DestroyWindow")
	pBeginPaint       = modUser32.NewProc("BeginPaint")
	pEndPaint         = modUser32.NewProc("EndPaint")
	pLoadCursorW      = modUser32.NewProc("LoadCursorW")
	pGetSystemMetrics = modUser32.NewProc("GetSystemMetrics")
	pSetTimer         = modUser32.NewProc("SetTimer")
	pGetClientRect    = modUser32.NewProc("GetClientRect")
	pSetForegroundWin = modUser32.NewProc("SetForegroundWindow")
	// Para trazer a janela PARA A FRENTE mesmo com a sessao remota em foco.
	pGetForegroundWin      = modUser32.NewProc("GetForegroundWindow")
	pGetWindowThreadPID    = modUser32.NewProc("GetWindowThreadProcessId")
	pAttachThreadInput     = modUser32.NewProc("AttachThreadInput")
	pBringWindowToTop      = modUser32.NewProc("BringWindowToTop")
	pSetWindowPos          = modUser32.NewProc("SetWindowPos")
	pSetActiveWindow       = modUser32.NewProc("SetActiveWindow")
	pFlashWindowEx         = modUser32.NewProc("FlashWindowEx")
	pSystemParametersInfoW = modUser32.NewProc("SystemParametersInfoW")

	pCreateSolidBrush = modGdi32.NewProc("CreateSolidBrush")

	pGdiplusStartup           = modGdiplus.NewProc("GdiplusStartup")
	pGdiplusShutdown          = modGdiplus.NewProc("GdiplusShutdown")
	pGdipCreateBitmapFromFile = modGdiplus.NewProc("GdipCreateBitmapFromFile")
	pGdipDisposeImage         = modGdiplus.NewProc("GdipDisposeImage")
	pGdipGetImageWidth        = modGdiplus.NewProc("GdipGetImageWidth")
	pGdipGetImageHeight       = modGdiplus.NewProc("GdipGetImageHeight")
	pGdipCreateFromHDC        = modGdiplus.NewProc("GdipCreateFromHDC")
	pGdipDeleteGraphics       = modGdiplus.NewProc("GdipDeleteGraphics")
	pGdipDrawImageRectI       = modGdiplus.NewProc("GdipDrawImageRectI")
	pGdipSetInterpolationMode = modGdiplus.NewProc("GdipSetInterpolationMode")

	pShellExecuteW = modShell32.NewProc("ShellExecuteW")

	pGetModuleHandleW  = modKernel32.NewProc("GetModuleHandleW")
	pGetCurrentThreadI = modKernel32.NewProc("GetCurrentThreadId")
)

// Constantes Win32 usadas.
const (
	_WS_OVERLAPPED     = 0x00000000
	_WS_CAPTION        = 0x00C00000
	_WS_SYSMENU        = 0x00080000
	_WS_VISIBLE        = 0x10000000
	_WS_EX_APPWINDOW   = 0x00040000
	_WS_EX_TOPMOST     = 0x00000008
	_CW_USEDEFAULT     = ^uintptr(0x7fffffff) // (int32)0x80000000
	_SW_SHOW           = 5
	// Trazer para a frente (driblar o lock de foreground do Windows).
	_HWND_TOPMOST                = ^uintptr(0) // (HWND)-1
	_SWP_NOSIZE                  = 0x0001
	_SWP_NOMOVE                  = 0x0002
	_SWP_SHOWWINDOW              = 0x0040
	_SPI_SETFOREGROUNDLOCKTIMEOUT = 0x2001
	_SPIF_SENDCHANGE             = 0x0002
	_FLASHW_ALL                  = 0x00000003
	_FLASHW_TIMERNOFG            = 0x0000000C
	_SM_CXSCREEN       = 0
	_SM_CYSCREEN       = 1
	_IDC_ARROW         = 32512
	_WM_DESTROY        = 0x0002
	_WM_PAINT          = 0x000F
	_WM_CLOSE          = 0x0010
	_WM_KEYDOWN        = 0x0100
	_WM_LBUTTONUP      = 0x0202
	_WM_TIMER          = 0x0113
	_VK_ESCAPE         = 0x1B
	_COLOR_dark        = 0x001A130B // COLORREF 0x00BBGGRR — fundo #0B131A (azul quase preto)
	_interpolationHQ   = 7          // InterpolationModeHighQualityBicubic
	_timerAutoClose    = 1
	_autoCloseMs       = 90 * 1000 // 90s: se o tecnico saiu, a janela nao fica pra sempre
)

// WNDCLASSEXW — layout exato esperado pela API.
type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type pointW struct{ X, Y int32 }
type msgW struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      pointW
}
type rectW struct{ Left, Top, Right, Bottom int32 }
type paintStruct struct {
	hdc         uintptr
	fErase      int32
	rcPaint     rectW
	fRestore    int32
	fIncUpdate  int32
	rgbReserved [32]byte
}

// Estado do processo-janela (um por processo: cada anuncio e um CreateProcessAsUser).
var (
	uiImage   uintptr // ponteiro GpImage do GDI+
	uiImgW    int32
	uiImgH    int32
	uiCtaURL  string
	uiToken   uintptr // token do GdiplusStartup
)

// rodarAnuncioUI e o ponto de entrada do subcomando `mostrar-anuncio`. NAO usa o
// agent.log (roda na sessao do usuario, nao no ProgramData do servico): erra
// calado de proposito — anuncio nunca e critico.
func rodarAnuncioUI(payloadPath string) {
	raw, err := os.ReadFile(payloadPath)
	// O arquivo e temporario e so serve a esta chamada: removido assim que lido.
	_ = os.Remove(payloadPath)
	if err != nil {
		return
	}
	var p anuncioPayload
	if json.Unmarshal(raw, &p) != nil || p.ImageFile == "" {
		return
	}
	uiCtaURL = p.CtaURL

	// GDI+ ligado para todo o tempo de vida do processo.
	type gdiplusStartupInput struct {
		GdiplusVersion           uint32
		DebugEventCallback       uintptr
		SuppressBackgroundThread int32
		SuppressExternalCodecs   int32
	}
	in := gdiplusStartupInput{GdiplusVersion: 1}
	if r, _, _ := pGdiplusStartup.Call(uintptr(unsafe.Pointer(&uiToken)), uintptr(unsafe.Pointer(&in)), 0); r != 0 {
		return // GDI+ nao subiu
	}
	defer pGdiplusShutdown.Call(uiToken)

	imgPath, _ := syscall.UTF16PtrFromString(p.ImageFile)
	if r, _, _ := pGdipCreateBitmapFromFile.Call(uintptr(unsafe.Pointer(imgPath)), uintptr(unsafe.Pointer(&uiImage))); r != 0 || uiImage == 0 {
		return // imagem nao carregou
	}
	defer pGdipDisposeImage.Call(uiImage)
	var w, h uint32
	pGdipGetImageWidth.Call(uiImage, uintptr(unsafe.Pointer(&w)))
	pGdipGetImageHeight.Call(uiImage, uintptr(unsafe.Pointer(&h)))
	uiImgW, uiImgH = int32(w), int32(h)
	if uiImgW <= 0 || uiImgH <= 0 {
		return
	}

	// A imagem e desenhada no tamanho da area de cliente. Calculamos a janela para
	// caber a arte inteira, mas nunca passar de ~80% da tela (arte grande em monitor
	// pequeno). Mantem proporcao.
	scrW, _, _ := pGetSystemMetrics.Call(_SM_CXSCREEN)
	scrH, _, _ := pGetSystemMetrics.Call(_SM_CYSCREEN)
	maxW := int32(scrW) * 80 / 100
	maxH := int32(scrH) * 80 / 100
	cw, ch := uiImgW, uiImgH
	if cw > maxW {
		ch = ch * maxW / cw
		cw = maxW
	}
	if ch > maxH {
		cw = cw * maxH / ch
		ch = maxH
	}

	hInst, _, _ := pGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("AcessoFastAnuncio")
	titulo := p.Titulo
	if titulo == "" {
		titulo = "AcessoFast"
	}
	titlePtr, _ := syscall.UTF16PtrFromString(titulo)
	cursor, _, _ := pLoadCursorW.Call(0, _IDC_ARROW)
	brush, _, _ := pCreateSolidBrush.Call(_COLOR_dark)

	wc := wndClassExW{
		style:         0,
		lpfnWndProc:   syscall.NewCallback(anuncioWndProc),
		hInstance:     hInst,
		hCursor:       cursor,
		hbrBackground: brush,
		lpszClassName: className,
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	if r, _, _ := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return
	}

	// Estilo: barra de titulo + menu de sistema (X), SEM redimensionar/maximizar —
	// e um cartaz, nao um editor. Nao topmost, nao fullscreen.
	style := uintptr(_WS_OVERLAPPED | _WS_CAPTION | _WS_SYSMENU | _WS_VISIBLE)

	// Ajusta o retangulo para que a AREA DE CLIENTE tenha cw x ch (a borda/barra
	// somam por fora). AdjustWindowRect faria isso; para simplicidade somamos uma
	// folga fixa da barra de titulo — a arte tolera alguns px.
	winW := cw + int32(scrW)*0 + 16
	winH := ch + 39 // ~altura de caption+borda
	px := (int32(scrW) - winW) / 2
	py := (int32(scrH) - winH) / 2

	// TOPMOST desde o nascimento: a sessao remota do tecnico esta em foco (muitas
	// vezes em tela cheia), e a janela precisa aparecer POR CIMA dela — senao o
	// tecnico ve a conexao cair sem entender o motivo. Topmost + o empurrao de foco
	// abaixo (trazParaFrente) e o que garante isso.
	hwnd, _, _ := pCreateWindowExW.Call(
		_WS_EX_APPWINDOW|_WS_EX_TOPMOST,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(titlePtr)),
		style,
		uintptr(px), uintptr(py), uintptr(winW), uintptr(winH),
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return
	}
	pShowWindow.Call(hwnd, _SW_SHOW)
	pUpdateWindow.Call(hwnd)
	trazParaFrente(hwnd)
	pSetTimer.Call(hwnd, _timerAutoClose, _autoCloseMs, 0)

	// Trava de seguranca: se por qualquer motivo o loop nao receber WM_QUIT, o
	// processo nao pode virar zumbi na sessao do usuario. Mata em autoCloseMs+30s.
	go func() {
		time.Sleep(time.Duration(_autoCloseMs)*time.Millisecond + 30*time.Second)
		os.Exit(0)
	}()

	var m msgW
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 { // 0 = WM_QUIT, -1 = erro
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

type flashWInfo struct {
	cbSize    uint32
	hwnd      uintptr
	dwFlags   uint32
	uCount    uint32
	dwTimeout uint32
}

// trazParaFrente forca a janela para o primeiro plano mesmo com outra janela
// (a sessao remota, muitas vezes em tela cheia) em foco. SetForegroundWindow
// sozinho e bloqueado pelo Windows quando o processo nao e dono do foco atual;
// o caminho confiavel e anexar a fila de input da thread em foco
// (AttachThreadInput) enquanto se pede o foco, reforcado por
// SetWindowPos(TOPMOST) + BringWindowToTop. FlashWindowEx e a rede de seguranca:
// se ainda assim nao subir, ao menos pisca chamando atencao.
func trazParaFrente(hwnd uintptr) {
	// Zera o timeout que o Windows usa para impedir "roubo" de foco.
	pSystemParametersInfoW.Call(_SPI_SETFOREGROUNDLOCKTIMEOUT, 0, 0, _SPIF_SENDCHANGE)

	pSetWindowPos.Call(hwnd, _HWND_TOPMOST, 0, 0, 0, 0, _SWP_NOMOVE|_SWP_NOSIZE|_SWP_SHOWWINDOW)

	fg, _, _ := pGetForegroundWin.Call()
	meu, _, _ := pGetCurrentThreadI.Call()
	var alvo uintptr
	if fg != 0 {
		alvo, _, _ = pGetWindowThreadPID.Call(fg, 0)
	}
	anexou := false
	if alvo != 0 && alvo != meu {
		if r, _, _ := pAttachThreadInput.Call(meu, alvo, 1); r != 0 {
			anexou = true
		}
	}
	pBringWindowToTop.Call(hwnd)
	pSetForegroundWin.Call(hwnd)
	pSetActiveWindow.Call(hwnd)
	if anexou {
		pAttachThreadInput.Call(meu, alvo, 0)
	}

	// Rede de seguranca: pisca (barra/janela) ate ganhar o foreground.
	fi := flashWInfo{hwnd: hwnd, dwFlags: _FLASHW_ALL | _FLASHW_TIMERNOFG, uCount: 3}
	fi.cbSize = uint32(unsafe.Sizeof(fi))
	pFlashWindowEx.Call(uintptr(unsafe.Pointer(&fi)))
}

func anuncioWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case _WM_PAINT:
		var ps paintStruct
		hdc, _, _ := pBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		var rc rectW
		pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
		var g uintptr
		if r, _, _ := pGdipCreateFromHDC.Call(hdc, uintptr(unsafe.Pointer(&g))); r == 0 && g != 0 {
			pGdipSetInterpolationMode.Call(g, _interpolationHQ)
			pGdipDrawImageRectI.Call(g, uiImage, 0, 0, uintptr(rc.Right-rc.Left), uintptr(rc.Bottom-rc.Top))
			pGdipDeleteGraphics.Call(g)
		}
		pEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0

	case _WM_LBUTTONUP:
		// Clique na arte -> abre o CTA no navegador padrao e fecha a janela.
		if uiCtaURL != "" {
			verb, _ := syscall.UTF16PtrFromString("open")
			url, _ := syscall.UTF16PtrFromString(uiCtaURL)
			pShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(url)), 0, 0, _SW_SHOW)
		}
		pDestroyWindow.Call(hwnd)
		return 0

	case _WM_KEYDOWN:
		if wParam == _VK_ESCAPE {
			pDestroyWindow.Call(hwnd)
		}
		return 0

	case _WM_TIMER:
		if wParam == _timerAutoClose {
			pDestroyWindow.Call(hwnd)
		}
		return 0

	case _WM_CLOSE:
		pDestroyWindow.Call(hwnd)
		return 0

	case _WM_DESTROY:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}
