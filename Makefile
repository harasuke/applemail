.PHONY: build test race clean install

PREFIX ?= /usr/local

build:
	go build -o mail ./cmd/mail

test:
	go test ./...

race:
	go test ./... -race

clean:
	rm -f mail

install: build
	install -m 0755 mail $(PREFIX)/bin/mail
	install -d $(PREFIX)/share/man/man1
	install -m 0644 docs/mail.1 $(PREFIX)/share/man/man1/mail.1
