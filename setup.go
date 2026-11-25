package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Versão do whisper.cpp usada nos binários prontos para Windows. Os hashes
// fixam exatamente o arquivo baixado: se o GitHub entregar outra coisa, a
// instalação para.
const whisperRelease = "b5907"

var whisperWindows = map[string]download{
	"amd64": {
		url:    "https://github.com/ggml-org/whisper.cpp/releases/download/" + whisperRelease + "/whisper-bin-x64.zip",
		sha256: "ecca07b277d943e9b9f4712856a144b857271a6b333104e511e1988e80db582b",
	},
	"arm64": {
		url:    "https://github.com/ggml-org/whisper.cpp/releases/download/" + whisperRelease + "/whisper-bin-win-cpu-arm64.zip",
		sha256: "4e522946827dcbc517d3cd7ee8d6cfe760e8143076924ac8513622694aa3ccfd",
	},
}

// Hashes conhecidos dos modelos. Outros modelos (small, medium...) também são
// baixados, mas só com HTTPS, sem conferência de hash.
var modelSHA256 = map[string]string{
	"ggml-base.bin":          "60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe",
	"ggml-silero-v6.2.0.bin": "2aa269b785eeb53a82983a20501ddf7c1d9c48e33ab63a41391ac6c9f7fb6987",
}

const (
	defaultModel = "base"
	vadModelFile = "ggml-silero-v6.2.0.bin"
)

type download struct{ url, sha256 string }

// dataDir é onde ficam modelos e o whisper baixados:
// Linux ~/.cache/yt-transcript, macOS ~/Library/Caches/yt-transcript,
// Windows %LocalAppData%\yt-transcript. YT_TRANSCRIPT_HOME muda o local.
func dataDir() string {
	if d := os.Getenv("YT_TRANSCRIPT_HOME"); d != "" {
		return d
	}
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "yt-transcript")
	}
	return filepath.Join(os.TempDir(), "yt-transcript")
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// searchDirs são as pastas onde procurar arquivos do projeto: ao lado do
// executável, um nível acima (bin/), a pasta atual e a pasta de dados.
func searchDirs() []string {
	exe := executableDir()
	return []string{exe, filepath.Dir(exe), ".", dataDir()}
}

func firstExisting(paths []string) string {
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// resolveWhisper acha o whisper-cli: caminho explícito, build local
// (make whisper), binário já baixado, PATH (brew/winget/apt) e, por fim, no
// Windows, baixa o binário oficial.
func resolveWhisper(ctx context.Context, explicit string, log *logger) (string, error) {
	if explicit != "" {
		if firstExisting([]string{explicit}) != "" {
			return explicit, nil
		}
		return "", fmt.Errorf("whisper-cli não encontrado em %q", explicit)
	}
	cli := exeName("whisper-cli")
	var candidates []string
	for _, d := range searchDirs() {
		candidates = append(candidates,
			filepath.Join(d, "third_party", "whisper.cpp", "build", "bin", cli),
			filepath.Join(d, "third_party", "whisper.cpp", "build", "bin", "Release", cli),
			filepath.Join(d, "whisper", cli),
		)
	}
	if p := firstExisting(candidates); p != "" {
		return p, nil
	}
	for _, name := range []string{"whisper-cli", "whisper-cpp"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	if dl, ok := whisperWindows[runtime.GOARCH]; ok && runtime.GOOS == "windows" {
		dir := filepath.Join(dataDir(), "whisper")
		log.printf("→ Baixando whisper.cpp %s (só na primeira vez)...", whisperRelease)
		if err := installWhisperZip(ctx, dl, dir, log); err != nil {
			return "", fmt.Errorf("não consegui instalar o whisper.cpp: %w", err)
		}
		return filepath.Join(dir, cli), nil
	}
	return "", errors.New("whisper-cli não encontrado. " + whisperHint())
}

func whisperHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "Instale com: brew install whisper-cpp"
	case "windows":
		return "Baixe whisper-bin-x64.zip em https://github.com/ggml-org/whisper.cpp/releases e use -whisper <caminho>\\whisper-cli.exe"
	default:
		return "Compile com `make whisper` na pasta do projeto, ou instale o pacote whisper-cpp da sua distribuição"
	}
}

// resolveModel aceita um nome (base, small, large-v3-turbo-q5_0...) ou um
// caminho para .bin. Nomes são procurados nas pastas conhecidas e, se não
// existirem, baixados para a pasta de dados.
func resolveModel(ctx context.Context, spec string, log *logger) (string, error) {
	if strings.HasSuffix(spec, ".bin") || strings.ContainsAny(spec, `/\`) {
		if firstExisting([]string{spec}) != "" {
			return spec, nil
		}
		return "", fmt.Errorf("modelo não encontrado em %q", spec)
	}
	return ensureModelFile(ctx, "ggml-"+spec+".bin",
		"https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-"+spec+".bin", log)
}

func resolveVAD(ctx context.Context, explicit string, log *logger) (string, error) {
	if explicit != "" {
		if firstExisting([]string{explicit}) != "" {
			return explicit, nil
		}
		return "", fmt.Errorf("modelo VAD não encontrado em %q", explicit)
	}
	return ensureModelFile(ctx, vadModelFile,
		"https://huggingface.co/ggml-org/whisper-vad/resolve/main/"+vadModelFile, log)
}

func ensureModelFile(ctx context.Context, file, url string, log *logger) (string, error) {
	var candidates []string
	for _, d := range searchDirs() {
		candidates = append(candidates, filepath.Join(d, "models", file))
	}
	if p := firstExisting(candidates); p != "" {
		return p, nil
	}
	dst := filepath.Join(dataDir(), "models", file)
	log.printf("→ Baixando %s (só na primeira vez)...", file)
	if err := fetch(ctx, download{url: url, sha256: modelSHA256[file]}, dst, log); err != nil {
		return "", fmt.Errorf("download de %s falhou: %w", file, err)
	}
	return dst, nil
}

// fetch baixa para dst.part, confere o SHA-256 (se conhecido) e só então
// renomeia: um download interrompido nunca vira um arquivo "válido".
func fetch(ctx context.Context, dl download, dst string, log *logger) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dl.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "yt-transcript")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s em %s", resp.Status, dl.url)
	}

	part := dst + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	h := sha256.New()
	pr := &progressReader{r: resp.Body, total: resp.ContentLength, log: log}
	_, err = io.Copy(io.MultiWriter(f, h), pr)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	log.progressDone()
	if err != nil {
		os.Remove(part)
		return err
	}
	if resp.ContentLength > 0 && pr.n != resp.ContentLength {
		os.Remove(part)
		return fmt.Errorf("download incompleto (%d de %d bytes)", pr.n, resp.ContentLength)
	}
	if dl.sha256 != "" {
		if got := hex.EncodeToString(h.Sum(nil)); got != dl.sha256 {
			os.Remove(part)
			return fmt.Errorf("SHA-256 não confere: esperado %s, recebido %s", dl.sha256, got)
		}
	}
	return os.Rename(part, dst)
}

type progressReader struct {
	r        io.Reader
	n, total int64
	log      *logger
	last     int
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if p.total > 0 {
		if pct := int(p.n * 100 / p.total); pct != p.last {
			p.last = pct
			p.log.progress(fmt.Sprint(pct))
		}
	}
	return n, err
}

// installWhisperZip baixa o zip oficial e extrai só os arquivos de Release/
// (whisper-cli.exe e DLLs) direto em dir.
func installWhisperZip(ctx context.Context, dl download, dir string, log *logger) error {
	tmp := filepath.Join(dataDir(), "whisper.zip")
	if err := fetch(ctx, dl, tmp, log); err != nil {
		return err
	}
	defer os.Remove(tmp)
	return extractRelease(tmp, dir)
}

func extractRelease(zipPath, dir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	found := false
	for _, zf := range zr.File {
		name := strings.ReplaceAll(zf.Name, `\`, "/")
		rel, ok := strings.CutPrefix(name, "Release/")
		// Só arquivos diretos de Release/: sem subpastas, sem "..".
		if !ok || rel == "" || strings.Contains(rel, "/") || rel == ".." || zf.FileInfo().IsDir() {
			continue
		}
		if err := extractFile(zf, filepath.Join(dir, rel)); err != nil {
			return err
		}
		if strings.EqualFold(rel, "whisper-cli.exe") {
			found = true
		}
	}
	if !found {
		return errors.New("whisper-cli.exe não está no pacote baixado")
	}
	return nil
}

func extractFile(zf *zip.File, dst string) error {
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, rc); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// toolHint diz como instalar yt-dlp e ffmpeg em cada sistema.
func toolHint(tool string) string {
	hints := map[string]map[string]string{
		"yt-dlp": {
			"darwin":  "brew install yt-dlp",
			"windows": "winget install yt-dlp.yt-dlp",
			"linux":   `pipx install "yt-dlp[default]"  (o pacote do apt costuma estar desatualizado)`,
		},
		"ffmpeg": {
			"darwin":  "brew install ffmpeg",
			"windows": "winget install Gyan.FFmpeg",
			"linux":   "sudo apt install ffmpeg  (ou dnf/pacman)",
		},
	}
	if h, ok := hints[tool][runtime.GOOS]; ok {
		return h
	}
	return hints[tool]["linux"]
}

func checkTools() error {
	var missing []string
	for _, t := range []string{"yt-dlp", "ffmpeg"} {
		if _, err := exec.LookPath(t); err != nil {
			missing = append(missing, fmt.Sprintf("%s não está instalado. Instale com: %s", t, toolHint(t)))
		}
	}
	if len(missing) > 0 {
		return errors.New(strings.Join(missing, "\n      "))
	}
	return nil
}

// runSetup prepara tudo de uma vez (útil antes do primeiro uso ou em CI):
// confere yt-dlp/ffmpeg, acha ou baixa o whisper e baixa os modelos.
func runSetup(ctx context.Context, opt options) error {
	log := newLogger(0, 1)
	ok := true
	for _, t := range []string{"yt-dlp", "ffmpeg"} {
		if p, err := exec.LookPath(t); err == nil {
			log.printf("✓ %-12s %s", t, p)
		} else {
			log.printf("✗ %-12s instale com: %s", t, toolHint(t))
			ok = false
		}
	}
	steps := []struct {
		name    string
		resolve func() (string, error)
	}{
		{"whisper-cli", func() (string, error) { return resolveWhisper(ctx, opt.whisper, log) }},
		{"modelo", func() (string, error) { return resolveModel(ctx, opt.model, log) }},
		{"VAD", func() (string, error) { return resolveVAD(ctx, opt.vadModel, log) }},
	}
	for _, s := range steps {
		p, err := s.resolve()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.printf("✗ %-12s %v", s.name, err)
			ok = false
			continue
		}
		log.printf("✓ %-12s %s", s.name, p)
	}
	if !ok {
		return errors.New("faltam dependências (veja acima)")
	}
	log.printf("Tudo pronto.")
	return nil
}
