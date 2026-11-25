package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"os"
	"path/filepath"
	"testing"
)

func TestSafeNameWindows(t *testing.T) {
	cases := map[string]string{
		"CON":            "_CON",
		"nul.txt":        "_nul.txt",
		"Com1":           "_Com1",
		"Console Wars":   "Console Wars",
		"Fim com ponto.": "Fim com ponto",
		"espaço no fim ": "espaço no fim",
	}
	for in, want := range cases {
		if got := safeName(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestExtractReleaseOnlyFlatFiles(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "w.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range []string{
		"Release/whisper-cli.exe",
		"Release/ggml.dll",
		"Release/../../fora.txt", // zip slip
		"Release/sub/aninhado.dll",
		"outro/ignorado.exe",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte("x"))
	}
	zw.Close()
	f.Close()

	out := filepath.Join(dir, "out")
	if err := extractRelease(zipPath, out); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(out)
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if len(got) != 2 || got[0] != "ggml.dll" || got[1] != "whisper-cli.exe" {
		t.Fatalf("arquivos extraídos: %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "fora.txt")); err == nil {
		t.Fatal("zip slip: arquivo escrito fora da pasta")
	}
}

func TestResolveModelExplicitMissing(t *testing.T) {
	if _, err := resolveModel(t.Context(), filepath.Join(t.TempDir(), "nao-existe.bin"), newLogger(0, 1)); err == nil {
		t.Fatal("esperava erro para caminho inexistente")
	}
}

func TestFetchVerifiesHash(t *testing.T) {
	body := []byte("conteúdo do modelo")
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/curto" {
			w.Header().Set("Content-Length", "100")
			w.Write(body) // servidor corta antes do fim
			return
		}
		w.Write(body)
	}))
	defer srv.Close()
	log := newLogger(0, 2)
	dir := t.TempDir()

	ok := filepath.Join(dir, "ok.bin")
	if err := fetch(t.Context(), download{srv.URL + "/m", hex.EncodeToString(sum[:])}, ok, log); err != nil {
		t.Fatalf("hash certo: %v", err)
	}
	bad := filepath.Join(dir, "bad.bin")
	if err := fetch(t.Context(), download{srv.URL + "/m", strings.Repeat("0", 64)}, bad, log); err == nil {
		t.Fatal("hash errado deveria falhar")
	}
	short := filepath.Join(dir, "short.bin")
	if err := fetch(t.Context(), download{srv.URL + "/curto", ""}, short, log); err == nil {
		t.Fatal("download incompleto deveria falhar")
	}
	for _, p := range []string{bad, short, bad + ".part", short + ".part"} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("sobrou arquivo de download que falhou: %s", p)
		}
	}
}
