package main

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

type subTrack struct {
	Ext string `json:"ext"`
}

type videoInfo struct {
	ID                string                `json:"id"`
	Title             string                `json:"title"`
	Language          string                `json:"language"`
	Duration          float64               `json:"duration"`
	Subtitles         map[string][]subTrack `json:"subtitles"`
	AutomaticCaptions map[string][]subTrack `json:"automatic_captions"`

	// infoPath aponta para o JSON completo salvo pelo yt-dlp. As chamadas
	// seguintes usam --load-info-json e não reabrem a página do vídeo
	// (economiza ~1,4 s por chamada).
	infoPath string
}

// fetchInfo lê os metadados do vídeo uma única vez e guarda o JSON em tmp.
func fetchInfo(ctx context.Context, url, tmp string, verbose bool) (*videoInfo, error) {
	cmd := command(ctx, "yt-dlp", "-J", "--no-playlist", "--no-warnings", "--", url)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if verbose {
		fmt.Fprint(os.Stderr, stderr.String())
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("yt-dlp não conseguiu ler o vídeo: %s", lastLine(stderr.String(), err))
	}
	var info videoInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("resposta inesperada do yt-dlp: %w", err)
	}
	info.infoPath = filepath.Join(tmp, "info.json")
	if err := os.WriteFile(info.infoPath, out, 0o600); err != nil {
		return nil, err
	}
	return &info, nil
}

// pickSubtitle escolhe a melhor legenda no idioma pedido: primeiro a manual,
// depois a automática no idioma original (nunca uma tradução automática).
func pickSubtitle(info *videoInfo, lang string) (key string, auto bool, ok bool) {
	if k := matchLang(info.Subtitles, lang); k != "" {
		return k, false, true
	}
	if _, found := info.AutomaticCaptions[lang+"-orig"]; found {
		return lang + "-orig", true, true
	}
	// Sem "-orig", a legenda automática só é original se o vídeo for desse idioma.
	if normalizeLang(info.Language) == lang {
		if _, found := info.AutomaticCaptions[lang]; found {
			return lang, true, true
		}
	}
	return "", false, false
}

// matchLang procura a legenda exata e depois variantes (pt → pt-BR, pt-PT).
// As chaves são ordenadas para o resultado não depender da ordem do map.
func matchLang(m map[string][]subTrack, lang string) string {
	if _, ok := m[lang]; ok {
		return lang
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if k != "live_chat" && normalizeLang(k) == lang {
			return k
		}
	}
	return ""
}

func downloadSubtitle(ctx context.Context, info *videoInfo, key string, auto bool, tmp string, verbose bool) ([]cue, error) {
	args := []string{"--load-info-json", info.infoPath, "--skip-download", "--no-warnings",
		"--sub-langs", key, "--sub-format", "vtt", "-o", filepath.Join(tmp, "sub")}
	if auto {
		args = append(args, "--write-auto-subs")
	} else {
		args = append(args, "--write-subs")
	}
	if err := runCmd(ctx, verbose, "yt-dlp", args...); err != nil {
		return nil, err
	}
	files, _ := filepath.Glob(filepath.Join(tmp, "sub*.vtt"))
	if len(files) == 0 {
		return nil, errors.New("o YouTube não entregou o arquivo da legenda")
	}
	f, err := os.Open(files[0])
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseVTT(f)
}

// downloadWAV liga o yt-dlp direto no ffmpeg por um pipe: a conversão para
// 16 kHz mono acontece enquanto o áudio chega, e o arquivo original
// (webm/m4a) nunca vai para o disco.
func downloadWAV(ctx context.Context, info *videoInfo, wav string, verbose bool) error {
	dl := command(ctx, "yt-dlp", "--load-info-json", info.infoPath, "-f", "bestaudio/best",
		"--no-warnings", "--no-progress", "-o", "-")
	ff := command(ctx, "ffmpeg", "-y", "-loglevel", "error", "-i", "pipe:0",
		"-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", wav)

	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	dl.Stdout, ff.Stdin = w, r
	var dlErr, ffErr strings.Builder
	if verbose {
		dl.Stderr, ff.Stderr, ff.Stdout = os.Stderr, os.Stderr, os.Stderr
	} else {
		dl.Stderr, ff.Stderr = &dlErr, &ffErr
	}

	if err := ff.Start(); err != nil {
		r.Close()
		w.Close()
		return fmt.Errorf("ffmpeg falhou: %w", err)
	}
	if err := dl.Start(); err != nil {
		r.Close()
		w.Close()
		_ = ff.Wait()
		return fmt.Errorf("yt-dlp falhou: %w", err)
	}
	// As pontas ficam só com os filhos; sem fechar aqui, o ffmpeg nunca
	// recebe EOF e o yt-dlp não percebe se o ffmpeg morrer.
	r.Close()
	w.Close()

	errDl, errFf := dl.Wait(), ff.Wait()
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errDl != nil:
		return fmt.Errorf("download do áudio falhou: %s", lastLine(dlErr.String(), errDl))
	case errFf != nil:
		return fmt.Errorf("ffmpeg falhou: %s", lastLine(ffErr.String(), errFf))
	}
	return nil
}

var progressRe = regexp.MustCompile(`progress\s*=\s*(\d+)%`)

// runWithProgress mostra só a porcentagem do whisper (ou tudo, com -v).
func runWithProgress(cmd *exec.Cmd, verbose bool, log *logger) error {
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if verbose {
		cmd.Stdout = os.Stderr
	} else {
		cmd.Stdout = io.Discard
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var tail ring
	sc := bufio.NewScanner(stderr)
	for sc.Scan() {
		line := sc.Text()
		if verbose {
			fmt.Fprintln(os.Stderr, line)
		} else if m := progressRe.FindStringSubmatch(line); m != nil {
			log.progress(m[1])
		}
		tail.add(line)
	}
	err = cmd.Wait()
	if !verbose {
		log.progressDone()
	}
	if err != nil {
		return fmt.Errorf("%v: %s", err, tail.join(" | "))
	}
	return nil
}

func runCmd(ctx context.Context, verbose bool, name string, args ...string) error {
	cmd := command(ctx, name, args...)
	var buf strings.Builder
	if verbose {
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	} else {
		cmd.Stdout, cmd.Stderr = io.Discard, &buf
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s falhou: %s", name, lastLine(buf.String(), err))
	}
	return nil
}

// command cria o processo ligado ao contexto (Ctrl+C encerra o filho) e com
// um ambiente limpo: caminhos do Nix em LD_LIBRARY_PATH, herdados de
// programas empacotados pelo Nix, quebram binários do sistema (ex.: ffmpeg do apt).
func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 5 * time.Second // não fica preso se um neto segurar o pipe
	env := os.Environ()
	for i, kv := range env {
		if v, ok := strings.CutPrefix(kv, "LD_LIBRARY_PATH="); ok {
			var keep []string
			for p := range strings.SplitSeq(v, string(os.PathListSeparator)) {
				if p != "" && !strings.HasPrefix(p, "/nix/store/") {
					keep = append(keep, p)
				}
			}
			env[i] = "LD_LIBRARY_PATH=" + strings.Join(keep, string(os.PathListSeparator))
		}
	}
	cmd.Env = env
	return cmd
}

// ring guarda as últimas linhas de saída para mensagens de erro.
type ring struct {
	buf [5]string
	n   int
}

func (r *ring) add(s string) { r.buf[r.n%len(r.buf)] = s; r.n++ }

func (r *ring) join(sep string) string {
	start, count := 0, r.n
	if r.n > len(r.buf) {
		start, count = r.n%len(r.buf), len(r.buf)
	}
	out := make([]string, 0, count)
	for i := range count {
		out = append(out, r.buf[(start+i)%len(r.buf)])
	}
	return strings.Join(out, sep)
}
