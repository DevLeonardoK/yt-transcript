GO      ?= $(shell command -v go 2>/dev/null || echo $(HOME)/.local/go/bin/go)
CMAKE   ?= $(shell command -v cmake 2>/dev/null || echo $(HOME)/.local/bin/cmake)
WHISPER_TAG := v1.9.6
MODEL   ?= base
VAD     := silero-v6.2.0
# binário estático (sem cgo), sem caminhos locais e sem tabela de debug: menor e reproduzível
GOFLAGS_BUILD := -trimpath -ldflags="-s -w"

.PHONY: all build whisper model vad test bench lint install clean

all: whisper build

build:
	CGO_ENABLED=0 $(GO) build $(GOFLAGS_BUILD) -o bin/yt-transcript .

test:
	$(GO) test -race ./...

bench:
	$(GO) test -run '^$$' -bench . -benchmem ./...

lint:
	$(GO) vet ./...
	$(GO) fix -diff ./...

whisper: third_party/whisper.cpp/build/bin/whisper-cli

third_party/whisper.cpp/build/bin/whisper-cli:
	[ -d third_party/whisper.cpp ] || git clone --depth 1 --branch $(WHISPER_TAG) https://github.com/ggml-org/whisper.cpp third_party/whisper.cpp
	cd third_party/whisper.cpp && $(CMAKE) -B build -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DWHISPER_BUILD_TESTS=OFF
	cd third_party/whisper.cpp && $(CMAKE) --build build -j --config Release --target whisper-cli

# make model MODEL=small  (tiny, base, small, medium, large-v3-turbo-q5_0...)
model: models/ggml-$(MODEL).bin

# detecção de voz: pula silêncio (mais rápido e com menos erros)
vad: models/ggml-$(VAD).bin

models/ggml-$(VAD).bin:
	mkdir -p models
	curl -fL -o $@.part https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-$(VAD).bin
	mv $@.part $@

models/ggml-%.bin:
	mkdir -p models
	curl -fL -o $@.part https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-$*.bin
	mv $@.part $@

# cria um atalho em ~/.local/bin para usar de qualquer pasta
install: build
	mkdir -p $(HOME)/.local/bin
	ln -sf $(CURDIR)/bin/yt-transcript $(HOME)/.local/bin/yt-transcript

clean:
	rm -rf bin
