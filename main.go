// yt-transcript transcreve vídeos do YouTube.
//
// Usa a legenda do próprio vídeo quando existe (rápido) e, quando não existe,
// baixa o áudio e transcreve localmente com whisper.cpp (sem internet para a IA,
// sem GPU).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type options struct {
	outDir   string
	lang     string
	model    string
	vadModel string
	whisper  string
	threads  int
	beam     int
	jobs     int
	forceAI  bool
	noVAD    bool
	srt      bool
	verbose  bool
	keepTemp bool
}

func main() {
	os.Exit(realMain())
}

// realMain fica separado de main para que os defers (limpeza de temporários,
// cancelamento) rodem antes do os.Exit.
func realMain() int {
	opt := options{}
	args := os.Args[1:]
	setup := len(args) > 0 && args[0] == "setup"
	if setup {
		args = args[1:]
	}

	fs := flag.NewFlagSet("yt-transcript", flag.ExitOnError)
	fs.StringVar(&opt.outDir, "o", ".", "pasta onde salvar a transcrição")
	fs.StringVar(&opt.lang, "lang", "auto", "idioma do vídeo (pt, en, es...). auto = usa o idioma informado pelo YouTube")
	fs.StringVar(&opt.model, "model", envOr("YT_WHISPER_MODEL", defaultModel), "modelo whisper: nome (tiny, base, small, medium, large-v3-turbo-q5_0) ou caminho .bin; nomes são baixados se faltarem")
	fs.StringVar(&opt.vadModel, "vad-model", os.Getenv("YT_WHISPER_VAD"), "caminho do modelo de detecção de voz (padrão: baixa o Silero)")
	fs.StringVar(&opt.whisper, "whisper", os.Getenv("YT_WHISPER_BIN"), "caminho do whisper-cli (padrão: build local, PATH ou download no Windows)")
	fs.IntVar(&opt.threads, "threads", defaultThreads(), "threads do whisper (padrão: núcleos físicos; hyperthreads deixam mais lento)")
	fs.IntVar(&opt.beam, "beam", 5, "beam size do whisper (1 = busca gulosa: mais rápido, um pouco menos preciso)")
	fs.IntVar(&opt.jobs, "j", 2, "vídeos processados em paralelo (a IA local roda um por vez)")
	fs.BoolVar(&opt.forceAI, "force-ai", false, "ignora legendas do YouTube e sempre transcreve com a IA local")
	fs.BoolVar(&opt.noVAD, "no-vad", false, "desliga a detecção de voz (transcreve também os silêncios)")
	fs.BoolVar(&opt.srt, "srt", false, "também gera arquivo .srt (legenda com tempo)")
	fs.BoolVar(&opt.verbose, "v", false, "mostra a saída completa do yt-dlp, ffmpeg e whisper")
	fs.BoolVar(&opt.keepTemp, "keep-temp", false, "não apaga os arquivos temporários (áudio, legendas brutas)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Uso:
  yt-transcript [opções] <link do YouTube> [mais links...]
  yt-transcript setup [opções]      confere e baixa as dependências

Os caminhos dos arquivos gerados saem no stdout (um por linha); o progresso vai para o stderr.

Opções:
`)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	// Ctrl+C cancela o contexto: os processos filhos são encerrados e os
	// temporários (áudio baixado) são apagados pelos defers.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if setup {
		if err := runSetup(ctx, opt); err != nil {
			logf("erro: %v", err)
			return 1
		}
		return 0
	}

	urls := fs.Args()
	if len(urls) == 0 {
		fs.Usage()
		return 2
	}
	opt.threads = max(opt.threads, 1)
	opt.beam = max(opt.beam, 1)
	opt.jobs = min(max(opt.jobs, 1), len(urls))

	if err := checkTools(); err != nil {
		logf("erro: %v", err)
		return 1
	}
	if err := os.MkdirAll(opt.outDir, 0o755); err != nil {
		logf("erro: %v", err)
		return 1
	}

	p := &pipeline{opt: opt, whisperSlot: make(chan struct{}, 1)}
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		failed int
		queue  = make(chan int)
	)
	for range opt.jobs {
		wg.Go(func() {
			for i := range queue {
				log := newLogger(i, len(urls))
				if err := p.run(ctx, urls[i], log); err != nil {
					log.printf("✗ %s: %v", urls[i], err)
					mu.Lock()
					failed++
					mu.Unlock()
				}
			}
		})
	}
	for i := range urls {
		if ctx.Err() != nil {
			break
		}
		queue <- i
	}
	close(queue)
	wg.Wait()

	if ctx.Err() != nil {
		logf("interrompido")
		return 130
	}
	if failed > 0 {
		return 1
	}
	return 0
}

type pipeline struct {
	opt options

	depsOnce sync.Once
	deps     whisperDeps
	depsErr  error

	// whisperSlot garante uma transcrição local por vez: o whisper já usa
	// todos os núcleos físicos, rodar dois juntos só deixa ambos mais lentos.
	// Downloads e legendas de outros vídeos seguem em paralelo.
	whisperSlot chan struct{}
}

func (p *pipeline) run(ctx context.Context, url string, log *logger) error {
	opt := p.opt
	start := time.Now()

	tmp, err := os.MkdirTemp("", "yt-transcript-*")
	if err != nil {
		return err
	}
	if opt.keepTemp {
		log.printf("  temporários em %s", tmp)
	} else {
		defer os.RemoveAll(tmp)
	}

	log.printf("→ Lendo informações do vídeo...")
	info, err := fetchInfo(ctx, url, tmp, opt.verbose)
	if err != nil {
		return err
	}
	log.printf("  %s (%s)", info.Title, fmtDuration(info.Duration))

	lang := normalizeLang(opt.lang)
	if lang == "auto" {
		lang = normalizeLang(info.Language)
	}

	var text, srt, source string

	if !opt.forceAI && lang != "" && lang != "auto" {
		if key, auto, ok := pickSubtitle(info, lang); ok {
			kind := "manual"
			if auto {
				kind = "automática"
			}
			log.printf("→ Usando legenda %s do YouTube (%s)...", kind, key)
			cues, err := downloadSubtitle(ctx, info, key, auto, tmp, opt.verbose)
			switch {
			case ctx.Err() != nil:
				return ctx.Err()
			case err != nil:
				log.printf("  falhou (%v), usando IA local", err)
			case len(cues) > 0:
				text, srt = cuesToText(cues), cuesToSRT(cues)
				source = "legenda " + kind + " do YouTube (" + key + ")"
			}
		} else {
			log.printf("  sem legenda em %q, usando IA local", lang)
		}
	}

	if text == "" {
		text, srt, err = p.transcribeLocal(ctx, info, lang, tmp, log)
		if err != nil {
			return err
		}
		source = "whisper.cpp (" + filepath.Base(p.deps.model) + ")"
	}

	base := filepath.Join(opt.outDir, safeName(info.Title)+" ["+safeName(info.ID)+"]")
	header := fmt.Sprintf("%s\nhttps://www.youtube.com/watch?v=%s\nFonte: %s\n\n", info.Title, info.ID, source)
	if err := os.WriteFile(base+".txt", []byte(header+text+"\n"), 0o644); err != nil {
		return err
	}
	log.printf("✓ %s.txt", base)
	printResult(base + ".txt")
	if opt.srt && srt != "" {
		if err := os.WriteFile(base+".srt", []byte(srt), 0o644); err != nil {
			return err
		}
		log.printf("✓ %s.srt", base)
		printResult(base + ".srt")
	}
	log.printf("  pronto em %s", time.Since(start).Round(100*time.Millisecond))
	return nil
}

type whisperDeps struct{ whisper, model, vad string }

func (p *pipeline) resolveDeps(ctx context.Context, log *logger) (whisperDeps, error) {
	var d whisperDeps
	var err error
	if d.whisper, err = resolveWhisper(ctx, p.opt.whisper, log); err != nil {
		return d, err
	}
	if d.model, err = resolveModel(ctx, p.opt.model, log); err != nil {
		return d, err
	}
	if !p.opt.noVAD {
		// Sem VAD a transcrição funciona, só fica mais lenta: não é erro fatal.
		if d.vad, err = resolveVAD(ctx, p.opt.vadModel, log); err != nil {
			if ctx.Err() != nil {
				return d, ctx.Err()
			}
			log.printf("  aviso: seguindo sem VAD (%v)", err)
		}
	}
	return d, nil
}

func (p *pipeline) transcribeLocal(ctx context.Context, info *videoInfo, lang, tmp string, log *logger) (text, srt string, err error) {
	opt := p.opt
	// Dependências da IA são resolvidas (e baixadas, se preciso) só quando
	// algum vídeo realmente precisa dela; um de cada vez.
	p.depsOnce.Do(func() { p.deps, p.depsErr = p.resolveDeps(ctx, log) })
	if p.depsErr != nil {
		return "", "", p.depsErr
	}
	deps := p.deps

	log.printf("→ Baixando e convertendo áudio...")
	wav := filepath.Join(tmp, "audio16k.wav")
	if err := downloadWAV(ctx, info, wav, opt.verbose); err != nil {
		return "", "", err
	}

	select {
	case p.whisperSlot <- struct{}{}:
		defer func() { <-p.whisperSlot }()
	case <-ctx.Done():
		return "", "", ctx.Err()
	}

	vad := deps.vad
	whisperLang := lang
	if whisperLang == "" {
		whisperLang = "auto"
	}
	extra := ""
	if vad != "" {
		extra = ", VAD"
	}
	log.printf("→ Transcrevendo com IA local (idioma: %s, %d threads%s)...", whisperLang, opt.threads, extra)

	outBase := filepath.Join(tmp, "out")
	args := []string{"-m", deps.model, "-f", wav, "-l", whisperLang,
		"-t", fmt.Sprint(opt.threads), "-bs", fmt.Sprint(opt.beam), "-bo", fmt.Sprint(opt.beam),
		"-otxt", "-osrt", "-of", outBase, "-np", "-pp"}
	if vad != "" {
		args = append(args, "--vad", "-vm", vad)
	}
	if err := runWithProgress(command(ctx, deps.whisper, args...), opt.verbose, log); err != nil {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		return "", "", fmt.Errorf("whisper falhou: %w", err)
	}

	txtBytes, err := os.ReadFile(outBase + ".txt")
	if err != nil {
		return "", "", err
	}
	srtBytes, err := os.ReadFile(outBase + ".srt")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	return joinLines(string(txtBytes)), string(srtBytes), nil
}

var stdoutMu sync.Mutex

// printResult escreve no stdout o caminho de um arquivo gerado. Scripts e
// agentes de IA leem daqui; mensagens de progresso ficam no stderr.
func printResult(path string) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	fmt.Println(path)
}
