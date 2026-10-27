package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	pngMinimo = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR")
	jpgMinimo = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
)

// O app abre o arquivo como imagem. Quem decide o que entra e o conteudo, nao o nome ou o
// content-type da resposta: um HTML de erro do storage nao pode virar a "logo" da maquina.
func TestEhImagemDeLogo(t *testing.T) {
	casos := []struct {
		nome string
		b    []byte
		quer bool
	}{
		{"png", pngMinimo, true},
		{"jpg", jpgMinimo, true},
		{"gif", []byte("GIF89a...."), false},
		{"html de erro", []byte("<html><body>403</body></html>"), false},
		{"vazio", nil, false},
	}
	for _, c := range casos {
		if got := ehImagemDeLogo(c.b); got != c.quer {
			t.Errorf("%s: ehImagemDeLogo = %v, queria %v", c.nome, got, c.quer)
		}
	}
}

// Gravar e ler de volta: a versao gravada e o que vai no proximo presence, e e por ela que o
// servidor decide nao mandar a mesma logo outra vez.
func TestGravaMarcaEVersao(t *testing.T) {
	dir := t.TempDir()
	if v := versaoNaMarca(dir); v != "" {
		t.Fatalf("pasta vazia deveria dar versao vazia, deu %q", v)
	}
	if err := gravaMarca(dir, "20261027T120000000000", "TecnoPonto Informática", pngMinimo); err != nil {
		t.Fatalf("gravaMarca: %v", err)
	}
	if v := versaoNaMarca(dir); v != "20261027T120000000000" {
		t.Fatalf("versao gravada = %q", v)
	}
	if err := tiraMarca(dir); err != nil {
		t.Fatalf("tiraMarca: %v", err)
	}
	if v := versaoNaMarca(dir); v != "" {
		t.Fatalf("depois de tirar a versao deveria ser vazia, deu %q", v)
	}
	// Tirar de novo, sem nada na pasta, nao e erro: o servidor pode mandar tirar duas vezes.
	if err := tiraMarca(dir); err != nil {
		t.Fatalf("tiraMarca em pasta vazia: %v", err)
	}
}

// Conteudo que nao e imagem nao grava nada — nem o json, que faria o app procurar uma logo
// que nao existe.
func TestGravaMarcaRecusaQueNaoEImagem(t *testing.T) {
	dir := t.TempDir()
	if err := gravaMarca(dir, "v1", "X", []byte("<html>")); err == nil {
		t.Fatal("gravou conteudo que nao e PNG nem JPG")
	}
	if _, err := os.Stat(filepath.Join(dir, marcaArquivo)); !os.IsNotExist(err) {
		t.Fatal("marca.json foi criado para uma imagem recusada")
	}
}

// json sem a imagem (apagada a mao, disco cheio no meio) nao conta como logo: a versao vazia
// faz o servidor mandar de novo, em vez de a maquina ficar presa sem logo para sempre.
func TestVersaoSemImagemNaoVale(t *testing.T) {
	dir := t.TempDir()
	if err := gravaMarca(dir, "v1", "X", jpgMinimo); err != nil {
		t.Fatalf("gravaMarca: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, marcaLogo)); err != nil {
		t.Fatal(err)
	}
	if v := versaoNaMarca(dir); v != "" {
		t.Fatalf("sem a imagem a versao deveria ser vazia, deu %q", v)
	}
}

// O nome vai direto para a tela do cliente: uma linha so e tamanho de rotulo.
func TestNomeDaMarca(t *testing.T) {
	if got := nomeDaMarca("  TecnoPonto\n  Informática \t"); got != "TecnoPonto Informática" {
		t.Errorf("espacos e quebras: %q", got)
	}
	longo := strings.Repeat("á", 120)
	if got := nomeDaMarca(longo); len([]rune(got)) != 80 {
		t.Errorf("nome longo ficou com %d caracteres, queria 80", len([]rune(got)))
	}
}
