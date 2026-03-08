# Chandra build system
#
# CGO is required (sqlite-vec, go-sqlite3).
# sqlite_fts5 enables FTS5 full-text search in go-sqlite3 for hybrid
# BM25+vector memory retrieval. Without it the binary degrades to pure
# vector search and logs a warning on startup.

GO      := go
TAGS    := sqlite_fts5
CGO     := CGO_ENABLED=1
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

BINDIR  := bin

.PHONY: all build chandrad chandra test test-all clean install

all: build

build: chandrad chandra

chandrad:
	$(CGO) $(GO) build -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o $(BINDIR)/chandrad ./cmd/chandrad

chandra:
	$(CGO) $(GO) build -tags "$(TAGS)" -ldflags "$(LDFLAGS)" -o $(BINDIR)/chandra ./cmd/chandra

test:
	$(CGO) $(GO) test -tags "$(TAGS)" ./...

test-all:
	$(CGO) $(GO) test -tags "$(TAGS)" -race -count=1 ./...

install: build
	sudo cp $(BINDIR)/chandrad /usr/local/bin/chandrad
	sudo cp $(BINDIR)/chandra  /usr/local/bin/chandra
	sudo cp scripts/chandrad-config-apply.sh /usr/local/bin/chandrad-config-apply
	sudo chmod +x /usr/local/bin/chandrad-config-apply

clean:
	rm -f $(BINDIR)/chandrad $(BINDIR)/chandra
