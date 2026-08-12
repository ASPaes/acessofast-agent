// sign-manifest assina o manifesto de auto-update de um build do agente.
//
// Roda SO no CI, no passo de release. A chave privada Ed25519 chega pelo ambiente
// (GitHub Actions secret) e nunca e escrita em disco nem impressa — em stdout sai
// apenas a assinatura, que e publica.
//
// A string assinada tem que ser byte a byte a mesma que o agente reconstroi em
// update.go (manifestoCanonico). Se as duas divergirem, todo update passa a ser
// reprovado — o que e a falha correta (o agente nao aceita o que nao consegue
// provar), mas silenciosa do lado de ca. Por isso o formato esta escrito uma vez
// so, aqui e la, e com o mesmo comentario.
//
//	acessofast-agent:v1:<version>:<sha256 minusculo>
//
// O prefixo com nome e versao do protocolo da separacao de dominio: impede que uma
// assinatura gerada pra outro proposito com a mesma chave valha como update.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	version := flag.String("version", "", "versao do build (AAAA.MM.DD-<sha7>)")
	sum := flag.String("sha256", "", "sha256 do binario, em hex")
	flag.Parse()

	if *version == "" || *sum == "" {
		fmt.Fprintln(os.Stderr, "uso: sign-manifest -version <v> -sha256 <hex>")
		os.Exit(2)
	}

	raw := strings.TrimSpace(os.Getenv("AGENT_UPDATE_SIGNING_KEY"))
	if raw == "" {
		fmt.Fprintln(os.Stderr, "AGENT_UPDATE_SIGNING_KEY vazio: sem chave nao ha release assinado.")
		os.Exit(1)
	}
	priv, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "AGENT_UPDATE_SIGNING_KEY nao e base64 valido")
		os.Exit(1)
	}
	if len(priv) != ed25519.PrivateKeySize {
		// Nao imprime a chave nem parte dela — so o tamanho, que ja identifica o erro
		// mais comum (ter colado a publica, de 32 bytes, no lugar da privada).
		fmt.Fprintf(os.Stderr, "chave privada tem %d bytes, esperado %d\n", len(priv), ed25519.PrivateKeySize)
		os.Exit(1)
	}

	msg := "acessofast-agent:v1:" + *version + ":" + strings.ToLower(strings.TrimSpace(*sum))
	sig := ed25519.Sign(ed25519.PrivateKey(priv), []byte(msg))

	// Só a assinatura em stdout, pra dar `SIG=$(sign-manifest ...)` no shell.
	fmt.Println(base64.StdEncoding.EncodeToString(sig))
}
