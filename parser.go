package main

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

type Parser struct {
	scanner   *bufio.Scanner
	delimiter []byte
	buf       []byte
}

func NewParser(r io.Reader) *Parser {
	scanner := bufio.NewScanner(r)
	return &Parser{scanner: scanner, delimiter: []byte("&!end")}
}

func (r *Parser) ParseHeader() map[string]string {
	h := map[string]string{}
	for r.scanner.Scan() {
		text := r.scanner.Text()
		if text == "" {
			continue
		} else if strings.HasPrefix(text, "&") {
			a := strings.SplitN(text[1:], ":", 2)
			if len(a) == 2 {
				h[a[0]] = a[1]
				continue
			}
		}
		if _, ok := h["title"]; !ok && strings.HasPrefix(text, "# ") {
			h["title"] = text[2:]
		}
		// Preserve the line separator when replaying the first non-header
		// line. Without it, a block directive at the start of a document
		// gets joined with the following line.
		r.buf = append([]byte(nil), r.scanner.Bytes()...)
		r.buf = append(r.buf, '\n')
		break
	}
	return h
}

func (r *Parser) Read(p []byte) (n int, err error) {
	if len(r.buf) == 0 {
		if !r.scanner.Scan() {
			return 0, io.EOF
		}
		r.buf = r.scanner.Bytes()
		if bytes.HasPrefix(r.buf, r.delimiter) {
			return 0, io.EOF
		}
		r.buf = append(r.buf, '\n')
	}
	l := copy(p, r.buf)
	r.buf = r.buf[l:]
	return l, nil
}
